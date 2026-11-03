package libre2

import (
	"errors"
	"sync"
	"testing"
	"time"
)

// fakeLink records what the handler sends.
type fakeLink struct {
	mu         sync.Mutex
	subscribed bool
	notifies   [][]byte
	closed     bool
}

func (l *fakeLink) Addr() string { return "test" }

func (l *fakeLink) Notify(c Char, b []byte) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if c != CharNotify || !l.subscribed {
		return errors.New("not subscribed")
	}
	l.notifies = append(l.notifies, append([]byte(nil), b...))
	return nil
}

func (l *fakeLink) Respond(c Char, b []byte) {}

func (l *fakeLink) Close() {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.closed = true
}

func (l *fakeLink) state() (notifies [][]byte, closed bool) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return append([][]byte(nil), l.notifies...), l.closed
}

// fakeClock is the handler's now, moved by the test.
type fakeClock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *fakeClock) now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

func (c *fakeClock) advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.t = c.t.Add(d)
}

// streamingSensor is a worn sensor that LibreTransmitter has enabled
// streaming on with code 42.
func streamingSensor(t *testing.T) (*Sensor, *State) {
	t.Helper()
	st := bleState(t, time.Hour)
	s := NewSensor(st, t.TempDir()+"/state.json")
	code, params := EnableStreamingCommand(st.uid(), st.patchInfo(), 42)
	if _, err := s.NFCCustom(code, params, time.Now()); err != nil {
		t.Fatal(err)
	}
	return s, st
}

type handlerRig struct {
	t     *testing.T
	s     *Sensor
	st    *State
	clock *fakeClock
	link  *fakeLink
	h     *handler
}

func newRig(t *testing.T, s *Sensor, st *State, clock *fakeClock) *handlerRig {
	link := &fakeLink{}
	h := newHandler(s, link, handlerTiming{tick: 2 * time.Millisecond, idle: unlockTimeout, now: clock.now})
	t.Cleanup(h.Closed)
	return &handlerRig{t: t, s: s, st: st, clock: clock, link: link, h: h}
}

// subscribe is the app enabling F002 notifications.
func (r *handlerRig) subscribe() {
	r.link.mu.Lock()
	r.link.subscribed = true
	r.link.mu.Unlock()
	r.h.Subscribed(CharNotify, true)
}

// unlock is Libre2DirectTransmitter writing its unlock for count n.
func (r *handlerRig) unlock(n uint16) {
	p := unlockPayload(r.st.uid(), r.st.patchInfo(), 42, n)
	r.h.Write(CharWrite, p[:])
}

// await waits for the link to have n notifies, or to close if n < 0.
func (r *handlerRig) await(n int) [][]byte {
	r.t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		got, closed := r.link.state()
		if n < 0 && closed || n >= 0 && len(got) >= n {
			return got
		}
		time.Sleep(time.Millisecond)
	}
	got, closed := r.link.state()
	r.t.Fatalf("waiting for %d notifies: have %d, closed %v", n, len(got), closed)
	return nil
}

// quiet checks that nothing more is sent and the link stays up for a while.
func (r *handlerRig) quiet(n int) {
	r.t.Helper()
	time.Sleep(30 * time.Millisecond)
	got, closed := r.link.state()
	if len(got) != n || closed {
		r.t.Fatalf("%d notifies (want %d), closed %v", len(got), n, closed)
	}
}

// packetAt reassembles notifies i…i+2 and checks them as the app would.
func (r *handlerRig) packetAt(notifies [][]byte, i, lc int) []byte {
	r.t.Helper()
	if len(notifies[i]) != 20 || len(notifies[i+1]) != 20 || len(notifies[i+2]) != 6 {
		r.t.Fatalf("notifies of %d, %d, %d bytes", len(notifies[i]), len(notifies[i+1]), len(notifies[i+2]))
	}
	var pkt []byte
	for _, n := range notifies[i : i+3] {
		pkt = append(pkt, n...)
	}
	checkPacket(r.t, r.st, pkt, lc)
	return pkt
}

func TestHandlerStreamsAfterUnlock(t *testing.T) {
	s, st := streamingSensor(t)
	clock := &fakeClock{t: time.Now()}
	r := newRig(t, s, st, clock)
	lc := st.lifeCount(clock.now())

	// LibreTransmitter finds F001 first and writes the unlock, then
	// subscribes to F002.
	r.unlock(1)
	r.quiet(0)
	r.subscribe()
	got := r.await(3)
	first := r.packetAt(got, 0, lc)
	if st.UnlockCount != 1 {
		t.Fatalf("unlock count %d, want 1", st.UnlockCount)
	}
	r.quiet(3)

	// The next minute brings the next packet, with another nonce.
	clock.advance(time.Minute)
	got = r.await(6)
	second := r.packetAt(got, 3, lc+1)
	if first[0] == second[0] && first[1] == second[1] {
		t.Fatal("two packets share a nonce")
	}
	r.quiet(6)

	// Out of range, the sensor hangs up.
	s.goOutOfRange(clock.now(), 5*time.Minute)
	r.await(-1)
	if s.Advertising(clock.now()) {
		t.Fatal("advertising while out of range")
	}
	clock.advance(6 * time.Minute)
	if !s.Advertising(clock.now()) {
		t.Fatal("not advertising back in range")
	}
}

func TestHandlerSubscribedBeforeUnlock(t *testing.T) {
	s, st := streamingSensor(t)
	clock := &fakeClock{t: time.Now()}
	r := newRig(t, s, st, clock)
	r.subscribe()
	r.quiet(0) // no unlock, nothing streams
	clock.advance(30 * time.Second)
	r.quiet(0)
	r.unlock(7)
	r.packetAt(r.await(3), 0, st.lifeCount(clock.now()))
}

func TestHandlerHangsUpWithoutUnlock(t *testing.T) {
	s, st := streamingSensor(t)
	clock := &fakeClock{t: time.Now()}
	r := newRig(t, s, st, clock)
	r.subscribe()
	clock.advance(59 * time.Second)
	r.quiet(0)
	clock.advance(time.Second)
	if got := r.await(-1); len(got) != 0 {
		t.Fatalf("%d notifies without an unlock", len(got))
	}
}

func TestHandlerRejectsBadUnlock(t *testing.T) {
	s, st := streamingSensor(t)
	good := unlockPayload(st.uid(), st.patchInfo(), 42, 1)
	bad := good
	bad[11] ^= 1
	wrongCode := unlockPayload(st.uid(), st.patchInfo(), 43, 1) // as if enabled with another code
	otherCount := unlockPayload(st.uid(), st.patchInfo(), 42, 2)
	spliced := good
	copy(spliced[4:], otherCount[4:]) // time says count 1, key is count 2's
	for name, p := range map[string][]byte{
		"flipped bit": bad[:], "other code": wrongCode[:], "spliced": spliced[:], "short": good[:11],
		"long": append(good[:], 0), "time before code": append([]byte{41, 0, 0, 0}, good[4:]...),
	} {
		clock := &fakeClock{t: time.Now()}
		r := newRig(t, s, st, clock)
		r.subscribe()
		r.h.Write(CharWrite, p)
		if got := r.await(-1); len(got) != 0 {
			t.Fatalf("%s: %d notifies", name, len(got))
		}
		if st.UnlockCount != 0 {
			t.Fatalf("%s: unlock count %d", name, st.UnlockCount)
		}
	}
}

func TestHandlerReplayedUnlock(t *testing.T) {
	for _, strict := range []bool{false, true} {
		s, st := streamingSensor(t)
		s.SetStrictUnlock(strict)
		clock := &fakeClock{t: time.Now()}
		lc := st.lifeCount(clock.now())

		r := newRig(t, s, st, clock)
		r.subscribe()
		r.unlock(1)
		r.packetAt(r.await(3), 0, lc)
		r.link.Close()
		r.h.Closed()

		// The same unlock again, as a replay would send it.
		r = newRig(t, s, st, clock)
		r.subscribe()
		r.unlock(1)
		if strict {
			if got := r.await(-1); len(got) != 0 {
				t.Fatalf("strict: replay streamed %d notifies", len(got))
			}
			// The app's next count still works.
			r = newRig(t, s, st, clock)
			r.subscribe()
			r.unlock(2)
		}
		r.packetAt(r.await(3), 0, lc)
		if want := map[bool]uint16{false: 1, true: 2}[strict]; st.UnlockCount != want {
			t.Fatalf("strict %v: unlock count %d, want %d", strict, st.UnlockCount, want)
		}
	}
}

func TestHandlerStopsWhenTheSensorEnds(t *testing.T) {
	s, st := streamingSensor(t)
	clock := &fakeClock{t: time.Now()}
	r := newRig(t, s, st, clock)
	r.unlock(1)
	r.subscribe()
	r.await(3)
	s.Terminate(clock.now())
	clock.advance(time.Minute)
	if got := r.await(-1); len(got) != 3 {
		t.Fatalf("%d notifies after terminating", len(got))
	}
	if s.Advertising(clock.now()) {
		t.Fatal("a terminated sensor advertises")
	}
}

func TestAdvertising(t *testing.T) {
	now := time.Now()
	st := bleState(t, time.Hour)
	s := NewSensor(st, t.TempDir()+"/state.json")
	if s.Advertising(now) {
		t.Fatal("advertising before streaming was enabled")
	}
	code, params := EnableStreamingCommand(st.uid(), st.patchInfo(), 42)
	if _, err := s.NFCCustom(code, params, now); err != nil {
		t.Fatal(err)
	}
	if !s.Advertising(now) {
		t.Fatal("not advertising once streaming is enabled")
	}
	s.LinkOpened()
	if s.Advertising(now) {
		t.Fatal("advertising while connected")
	}
	s.LinkClosed()
	if !s.Advertising(now) || s.Advertising(st.Activated.Add(time.Duration(st.WearMinutes)*time.Minute)) {
		t.Fatal("advertising wrong around expiry")
	}
}
