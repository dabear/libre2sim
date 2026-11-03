package libre2

import (
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
	opened     time.Time
	subscribed bool // the central enabled F002 notifications
	streaming  bool // a valid unlock arrived
	sent       int  // life count of the last packet sent; -1 for none
	done       bool // the sensor hung up
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
	n, err := h.s.unlock(b, now)
	if err != nil {
		h.hangUp("bad unlock: " + err.Error())
		return
	}
	log.Printf("%s unlocked streaming with count %d", h.link.Addr(), n)
	h.streaming = true
	h.sent = -1
	h.send(now)
}

func (h *handler) subscribe(c Char, enabled bool) {
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
	pkt := h.s.packet(lc)
	for _, chunk := range notifyChunks(pkt) {
		if err := h.link.Notify(CharNotify, chunk); err != nil {
			log.Printf("%s packet for minute %d: %v", h.link.Addr(), lc, err)
			return
		}
	}
	log.Printf("%s sent the packet for minute %d", h.link.Addr(), lc)
}

func (h *handler) hangUp(why string) {
	if h.done {
		return
	}
	h.done = true
	log.Printf("%s hanging up: %s", h.link.Addr(), why)
	h.link.Close()
	h.Closed()
}
