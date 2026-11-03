package libre2

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"time"
)

// The sensor's BLE side, as LibreTransmitter's Libre2DirectTransmitter and
// LibreTransmitterProxyManager (@ 4110512) use it: after an NFC
// enable-streaming the sensor advertises its address as its name with
// service FDE3; the app connects, writes a 12-byte unlock to F001 and
// subscribes to F002, and the sensor then notifies a 46-byte packet every
// minute.

// running is whether the sensor is worn and working at now, with mu held:
// activated, not terminated and not worn out.
func (s *State) running(now time.Time) bool {
	return s.Activated != nil && s.Terminated == nil && s.lifeCount(now) < s.WearMinutes
}

// SetStrictUnlock makes the sensor accept only unlock counts above the last
// one it accepted, so a replayed unlock fails. Off by default: whether a real
// Libre 2 checks this is not known, and LibreTransmitter's count only ever
// goes up, so either way it works with the app.
func (s *Sensor) SetStrictUnlock(on bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.strictUnlock = on
}

// Advertising is whether the sensor advertises at now: it is running,
// streaming was enabled over NFC (LibreTransmitter needs the address the
// enable-streaming command returns to recognise the advert), no central
// holds its one link and it is not out of range. A sensor that has ended
// (terminated or worn out) stays silent (sim's choice: it has nothing more
// to send).
func (s *Sensor) Advertising(now time.Time) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.advertising(now)
}

func (s *Sensor) advertising(now time.Time) bool {
	return s.state.running(now) && s.state.StreamingCode != nil && s.links == 0 && !now.Before(s.outOfRange)
}

// InRange is whether the phone can hear the sensor at now.
func (s *Sensor) InRange(now time.Time) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return !now.Before(s.outOfRange)
}

// GoOutOfRange makes the sensor unreachable for d from now: a link it holds
// drops and it does not advertise until then.
func (s *Sensor) GoOutOfRange(d time.Duration) { s.goOutOfRange(time.Now(), d) }

func (s *Sensor) goOutOfRange(now time.Time, d time.Duration) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.outOfRange = now.Add(d)
}

// LinkOpened and LinkClosed track the sensor's one link: it does not
// advertise while a central holds it. They count, so a link that finishes
// closing after the next one opened cannot leave the sensor advertising.
func (s *Sensor) LinkOpened() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.links++
}

func (s *Sensor) LinkClosed() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.links > 0 {
		s.links--
	}
}

// unlockSize is the length of Libre2.streamingUnlockPayload.
const unlockSize = 12

// unlock checks a write to F001 against the code the last enable-streaming
// command set. The app writes streamingUnlockPayload(uid, info, code, n):
// time = code + n LE4, then 8 key bytes. The sensor recovers n as
// time − code (wrapping in 32 bits, and it must fit the app's UInt16),
// recomputes the payload and compares the key bytes. In strict mode n must
// also be above the last accepted count. On success it saves n as
// UnlockCount and returns it.
func (s *Sensor) unlock(b []byte, now time.Time) (uint16, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	st := s.state
	switch {
	case len(b) != unlockSize:
		return 0, fmt.Errorf("unlock is %d bytes, want %d", len(b), unlockSize)
	case st.StreamingCode == nil:
		return 0, fmt.Errorf("streaming was never enabled over NFC")
	case !st.running(now):
		return 0, fmt.Errorf("the sensor has ended")
	}
	code := *st.StreamingCode
	t := binary.LittleEndian.Uint32(b[:4])
	n := t - code
	if n > 0xffff {
		return 0, fmt.Errorf("unlock time %d is not code %d plus a 16-bit count", t, code)
	}
	want := unlockPayload(st.uid(), st.patchInfo(), code, uint16(n))
	if !bytes.Equal(b[4:], want[4:]) {
		return 0, fmt.Errorf("unlock key %x for count %d, want %x", b[4:], n, want[4:])
	}
	if s.strictUnlock && uint16(n) <= st.UnlockCount {
		return 0, fmt.Errorf("unlock count %d replayed: the last accepted was %d", n, st.UnlockCount)
	}
	st.UnlockCount = uint16(n)
	s.save()
	return uint16(n), nil
}

// streamState is what a link needs to know at now: whether the sensor is
// still running, whether it is in range, and its life count.
func (s *Sensor) streamState(now time.Time) (running, inRange bool, lc int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.state.running(now), !now.Before(s.outOfRange), s.state.lifeCount(now)
}

// Connected is whether a central holds the link.
func (s *Sensor) Connected() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.links > 0
}
