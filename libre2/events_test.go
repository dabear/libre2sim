package libre2

import (
	"sync"
	"testing"
	"time"
)

func TestEventLogRing(t *testing.T) {
	l := NewEventLog(3)
	if ev, last := l.Since(0); len(ev) != 0 || last != 0 {
		t.Fatalf("empty log: %v, last %d", ev, last)
	}
	for i := 1; i <= 5; i++ {
		if seq := l.Add(CatBLE, "k", "event %d", i); seq != uint64(i) {
			t.Fatalf("event %d got seq %d", i, seq)
		}
	}
	ev, last := l.Since(0)
	if last != 5 || len(ev) != 3 || ev[0].Seq != 3 || ev[2].Seq != 5 || ev[0].Msg != "event 3" {
		t.Fatalf("after wrapping: last %d, %+v", last, ev)
	}
	if ev, _ := l.Since(4); len(ev) != 1 || ev[0].Seq != 5 {
		t.Errorf("since 4: %+v", ev)
	}
	if ev, last := l.Since(5); len(ev) != 0 || last != 5 {
		t.Errorf("since 5: %+v, last %d", ev, last)
	}
	l.Add(CatNFC, "k", "%d%% sure", 100)
	if ev, _ := l.Since(5); ev[0].Msg != "100% sure" {
		t.Errorf("message %q", ev[0].Msg)
	}
}

func TestEventLogConcurrent(t *testing.T) {
	l := NewEventLog(50)
	var wg sync.WaitGroup
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 100; i++ {
				l.Add(CatSensor, "k", "n")
				l.Since(0)
			}
		}()
	}
	wg.Wait()
	ev, last := l.Since(0)
	if last != 800 || len(ev) != 50 {
		t.Fatalf("last %d, %d events", last, len(ev))
	}
	for i := 1; i < len(ev); i++ {
		if ev[i].Seq != ev[i-1].Seq+1 {
			t.Fatalf("sequence broken at %d: %d then %d", i, ev[i-1].Seq, ev[i].Seq)
		}
	}
}

// The sensor records what happens to it.
func TestSensorEvents(t *testing.T) {
	st, err := NewState(15, time.Hour, 3*time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	s := NewSensor(st, t.TempDir()+"/state.json")
	s.SetGlucoseMode(GlucoseMode{Kind: "fixed", Value: 90})
	s.ArmFault(FaultBadCRC)
	s.notifies(10)
	s.GoOutOfRange(time.Minute)
	s.Terminate(time.Now())
	ev, _ := s.Events().Since(0)
	var kinds []string
	for _, e := range ev {
		kinds = append(kinds, e.Kind)
	}
	want := []string{"glucose-mode", "fault-armed", "fault-fired", "out-of-range", "terminated"}
	if len(kinds) != len(want) {
		t.Fatalf("events %v, want %v", kinds, want)
	}
	for i := range want {
		if kinds[i] != want[i] || ev[i].Cat != CatSensor {
			t.Errorf("event %d is %s/%s, want sensor/%s", i, ev[i].Cat, kinds[i], want[i])
		}
	}
}
