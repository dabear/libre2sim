package libre2

import (
	"testing"
	"time"
)

func joinChunks(chunks [][]byte) []byte {
	var b []byte
	for _, c := range chunks {
		b = append(b, c...)
	}
	return b
}

func TestArmFaultValidation(t *testing.T) {
	s := NewSensor(bleState(t, time.Hour), t.TempDir()+"/state.json")
	if err := s.ArmFault("melt"); err == nil {
		t.Fatal("unknown fault accepted")
	}
	if s.Fault() != "" {
		t.Fatalf("fault %q after a refused one", s.Fault())
	}
	for _, k := range []string{FaultBadCRC, FaultShort} {
		if err := s.ArmFault(k); err != nil || s.Fault() != k {
			t.Fatalf("arming %s: %v, fault %q", k, err, s.Fault())
		}
		if got := s.Status()["fault"]; got != k {
			t.Errorf("status fault %v, want %s", got, k)
		}
	}
	if err := s.ArmFault(FaultNone); err != nil || s.Fault() != "" {
		t.Fatalf("disarming: %v, fault %q", err, s.Fault())
	}
	if _, ok := s.Status()["fault"]; ok {
		t.Error("status reports a fault that is not armed")
	}
}

// badcrc spoils the next packet's CRC and only that one.
func TestFaultBadCRC(t *testing.T) {
	st := bleState(t, time.Hour)
	s := NewSensor(st, t.TempDir()+"/state.json")
	if err := s.ArmFault(FaultBadCRC); err != nil {
		t.Fatal(err)
	}
	bad := joinChunks(s.notifies(180))
	if len(bad) != packetSize {
		t.Fatalf("faulty packet is %d bytes, want %d", len(bad), packetSize)
	}
	if _, err := decryptPacket(st.uid(), bad); err != errPacketCRC {
		t.Fatalf("decrypting the faulty packet: %v, want the CRC error", err)
	}
	if s.Fault() != "" {
		t.Fatalf("fault %q still armed after one packet", s.Fault())
	}
	checkPacket(t, st, joinChunks(s.notifies(181)), 181)
}

// short sends 45 bytes as 20 + 20 + 5, once.
func TestFaultShort(t *testing.T) {
	st := bleState(t, time.Hour)
	s := NewSensor(st, t.TempDir()+"/state.json")
	if err := s.ArmFault(FaultShort); err != nil {
		t.Fatal(err)
	}
	chunks := s.notifies(180)
	if len(chunks) != 3 || len(chunks[0]) != 20 || len(chunks[1]) != 20 || len(chunks[2]) != 5 {
		t.Fatalf("chunks of %d bytes: %v", len(joinChunks(chunks)), chunks)
	}
	if _, err := decryptPacket(st.uid(), joinChunks(chunks)); err == nil {
		t.Fatal("a 45-byte packet decrypted")
	}
	if s.Fault() != "" {
		t.Fatalf("fault %q still armed", s.Fault())
	}
	chunks = s.notifies(181)
	if len(chunks) != 3 || len(chunks[2]) != 6 {
		t.Fatalf("next packet in chunks of %d, %d, %d", len(chunks[0]), len(chunks[1]), len(chunks[2]))
	}
	checkPacket(t, st, joinChunks(chunks), 181)
}

// Through a link: the armed fault hits the next minute's packet, and the one
// after is normal.
func TestHandlerFault(t *testing.T) {
	for _, kind := range []string{FaultBadCRC, FaultShort} {
		s, st := streamingSensor(t)
		clock := &fakeClock{t: time.Now()}
		r := newRig(t, s, st, clock)
		lc := st.lifeCount(clock.now())

		r.subscribe()
		r.unlock(1)
		got := r.await(3)
		r.packetAt(got, 0, lc)
		if err := s.ArmFault(kind); err != nil {
			t.Fatal(err)
		}
		clock.advance(time.Minute)
		got = r.await(6)
		var bad []byte
		for _, n := range got[3:6] {
			bad = append(bad, n...)
		}
		switch kind {
		case FaultBadCRC:
			if len(bad) != packetSize {
				t.Errorf("badcrc: %d bytes", len(bad))
			} else if _, err := decryptPacket(st.uid(), bad); err != errPacketCRC {
				t.Errorf("badcrc: %v", err)
			}
		case FaultShort:
			if len(got[3]) != 20 || len(got[4]) != 20 || len(got[5]) != 5 {
				t.Errorf("short: notifies of %d, %d, %d bytes", len(got[3]), len(got[4]), len(got[5]))
			}
		}
		clock.advance(time.Minute)
		got = r.await(9)
		r.packetAt(got, 6, lc+2)
		r.quiet(9)
	}
}
