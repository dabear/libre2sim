package libre2

import (
	"errors"
	"fmt"
	"log"
	"sync"
	"time"
)

// handlerTiming is the clock a handler runs on. Tests shorten the tick and
// drive now.
type handlerTiming struct {
	tick time.Duration    // how often the handler looks at the sensor
	idle time.Duration    // how long a link may go without a valid unlock
	now  func() time.Time // the time at each look
}

// unlockTimeout is how long the sensor waits for a valid unlock before
// hanging up: libre3sim's idle rule for a link that never authorizes (sim's
// choice; LibreTransmitter writes the unlock as soon as it finds F001).
const unlockTimeout = 60 * time.Second

var defaultTiming = handlerTiming{tick: time.Second, idle: unlockTimeout, now: time.Now}

// handler is the sensor's side of one link. The transport's calls are queued
// to the handler's own goroutine, which also runs its clock, so its fields
// need no lock.
type handler struct {
	s    *Sensor
	link Link
	t    handlerTiming

	events chan func()
	quit   chan struct{}
	once   sync.Once

	// Owned by run's goroutine.
	opened      time.Time
	subscribed  bool // the central enabled F002 notifications
	loginNotify bool // the central enabled F001 notifications (Gen2 login)
	streaming   bool // a valid unlock arrived
	sent        int  // life count of the last packet sent; -1 for none
	done        bool // the sensor hung up
}

// NewHandler starts the sensor's side of a new link:
//
//   - The central subscribing to F002 is remembered.
//   - A 12-byte write to F001 is checked as Sensor.unlock does. A bad unlock
//     makes the sensor hang up (sim's choice: a real sensor's answer is
//     unknown; dropping the link makes the failure visible in the app's log).
//     A good one starts streaming: a packet now, if F002 is subscribed (else
//     as soon as it is), then one for every new life-count minute.
//   - With no valid unlock within 60 s, the sensor hangs up.
//   - Out of range, the sensor hangs up.
//   - Once the sensor has ended (terminated or worn out) it sends nothing
//     more and hangs up (sim's choice).
//
// A Libre 2 sends a packet as three notifies of 20, 20 and 6 bytes.
//
// In Gen2 mode (gen2_mode.go) the unlock is a login on F001 instead, as
// LibreTransmitter's Libre2DirectTransmitter and Juggluco's
// Libre2GattCallback both run it: the central enables F001 notifications and
// writes 0x20; the sensor notifies a 14-byte challenge on F001; the central
// writes the 19-byte auth request; the sensor notifies the 25-byte response
// on F001 as 7 + 18 bytes and starts streaming frames on F002 as 20 + 18 + 8.
// A login that fails makes the sensor hang up, as a bad unlock does (sim's
// choice).
func NewHandler(s *Sensor, l Link) Handler { return newHandler(s, l, defaultTiming) }

func newHandler(s *Sensor, l Link, t handlerTiming) *handler {
	h := &handler{s: s, link: l, t: t, events: make(chan func()), quit: make(chan struct{}),
		opened: t.now(), sent: -1}
	go h.run()
	return h
}

func (h *handler) run() {
	tk := time.NewTicker(h.t.tick)
	defer tk.Stop()
	for !h.done {
		select {
		case f := <-h.events:
			f()
		case <-tk.C:
			h.tick(h.t.now())
		case <-h.quit:
			return
		}
	}
}

// post hands f to the handler's goroutine, unless the link has ended.
func (h *handler) post(f func()) {
	select {
	case h.events <- f:
	case <-h.quit:
	}
}

func (h *handler) Write(c Char, data []byte) {
	b := append([]byte(nil), data...)
	h.post(func() { h.write(c, b) })
}

func (h *handler) Subscribed(c Char, enabled bool) {
	h.post(func() { h.subscribe(c, enabled) })
}

// Read answers a read with no value: neither characteristic is readable.
func (h *handler) Read(c Char) {
	h.post(func() {
		log.Printf("%s read %s, which is not readable", h.link.Addr(), c)
		h.link.Respond(c, nil)
	})
}

// Closed ends the handler; the transport calls it once the link is gone.
func (h *handler) Closed() { h.once.Do(func() { close(h.quit) }) }

func (h *handler) write(c Char, b []byte) {
	if c != CharWrite {
		log.Printf("%s wrote %x to %s; ignored", h.link.Addr(), b, c)
		return
	}
	now := h.t.now()
	if h.s.Gen2() {
		h.writeGen2(b, now)
		return
	}
	n, err := h.s.unlock(b, now)
	if err != nil {
		k := h.s.unlockResult(false)
		h.s.events.Add(CatBLE, "unlock-refused", "unlock refused (%d so far): %v", k, err)
		h.hangUp("bad unlock: " + err.Error())
		return
	}
	log.Printf("%s unlocked streaming with count %d", h.link.Addr(), n)
	k := h.s.unlockResult(true)
	h.s.events.Add(CatBLE, "unlock-accepted", "unlock accepted, count %d (%d so far)", n, k)
	h.streaming = true
	h.sent = -1
	h.send(now)
}

func (h *handler) subscribe(c Char, enabled bool) {
	if c == CharWrite {
		h.loginNotify = enabled
		return
	}
	if c != CharNotify {
		return
	}
	h.subscribed = enabled
	if enabled {
		h.send(h.t.now())
	}
}

func (h *handler) tick(now time.Time) {
	running, inRange, _ := h.s.streamState(now)
	switch {
	case !inRange:
		h.hangUp("out of range")
	case !running:
		h.hangUp("the sensor has ended")
	case !h.streaming && now.Sub(h.opened) >= h.t.idle:
		h.hangUp("no valid unlock in " + h.t.idle.String())
	default:
		h.send(now)
	}
}

// send notifies the packet for the current minute, once, while streaming to
// a subscribed central.
func (h *handler) send(now time.Time) {
	if !h.streaming || !h.subscribed || h.done {
		return
	}
	running, _, lc := h.s.streamState(now)
	if !running || lc == h.sent {
		return
	}
	h.sent = lc
	chunks := h.s.notifies(lc)
	if len(chunks) == 0 {
		return
	}
	for _, chunk := range chunks {
		if err := h.link.Notify(CharNotify, chunk); err != nil {
			log.Printf("%s packet for minute %d: %v", h.link.Addr(), lc, err)
			return
		}
	}
	log.Printf("%s sent the packet for minute %d", h.link.Addr(), lc)
	h.s.events.Add(CatBLE, "packet", "packet sent for minute %d: %d mg/dL", lc, h.s.Reading(lc))
}

func (h *handler) hangUp(why string) {
	if h.done {
		return
	}
	h.done = true
	log.Printf("%s hanging up: %s", h.link.Addr(), why)
	h.s.events.Add(CatBLE, "hang-up", "sensor hangs up: %s", why)
	h.link.Close()
	h.Closed()
}

// gen2ChallengeRequest is what a Gen2 central writes to F001 to ask for a
// login challenge ("read security challenge").
const gen2ChallengeRequest = 0x20

// writeGen2 is a write to F001 on a Gen2 sensor: 0x20 asks for a challenge,
// 19 bytes answer it.
func (h *handler) writeGen2(b []byte, now time.Time) {
	refuse := func(err error) {
		k := h.s.unlockResult(false)
		h.s.events.Add(CatBLE, "unlock-refused", "Gen2 login refused (%d so far): %v", k, err)
		h.hangUp("bad Gen2 login: " + err.Error())
	}
	switch {
	case len(b) == 1 && b[0] == gen2ChallengeRequest:
		if !h.loginNotify {
			// Both references enable F001 notifications first (sim's
			// choice: without them the challenge has nowhere to go).
			refuse(errors.New("challenge asked for before F001 notifications were enabled"))
			return
		}
		ch, err := h.s.gen2Challenge(now)
		if err != nil {
			refuse(err)
			return
		}
		h.streaming = false
		if err := h.link.Notify(CharWrite, ch); err != nil {
			log.Printf("%s Gen2 challenge: %v", h.link.Addr(), err)
			return
		}
		h.s.events.Add(CatBLE, "gen2-challenge", "Gen2 login: challenge sent")
	case len(b) == Gen2AuthRequestSize:
		resp, err := h.s.gen2Accept(b, now)
		if err != nil {
			refuse(err)
			return
		}
		for _, chunk := range [][]byte{resp[:7], resp[7:]} {
			if err := h.link.Notify(CharWrite, chunk); err != nil {
				log.Printf("%s Gen2 login response: %v", h.link.Addr(), err)
				return
			}
		}
		log.Printf("%s Gen2 login accepted", h.link.Addr())
		k := h.s.unlockResult(true)
		h.s.events.Add(CatBLE, "unlock-accepted", "Gen2 login accepted (%d so far)", k)
		h.streaming = true
		h.sent = -1
		h.send(now)
	default:
		refuse(fmt.Errorf("unexpected %d-byte write %x", len(b), b))
	}
}
