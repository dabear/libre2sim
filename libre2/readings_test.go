package libre2

import (
	"math"
	"testing"
	"time"
)

func readingsSensor(t *testing.T, age, warmup time.Duration, mode GlucoseMode) (*Sensor, time.Time) {
	t.Helper()
	st, err := NewState(15, warmup, age)
	if err != nil {
		t.Fatal(err)
	}
	st.Glucose = mode
	s := NewSensor(st, t.TempDir()+"/state.json")
	return s, time.Now()
}

func nextOf(t *testing.T, s *Sensor, now time.Time) *NextReading {
	t.Helper()
	n, _ := s.Status()["nextReading"].(*NextReading)
	return n
}

// Fixed and sine are pure functions of the minute: the next reading is
// exactly what that minute then holds.
func TestNextReadingDeterministic(t *testing.T) {
	for _, mode := range []GlucoseMode{
		{Kind: "fixed", Value: 117},
		{Kind: "sine", Value: 120, Amplitude: 40, PeriodMinutes: 90},
	} {
		s, now := readingsSensor(t, 3*time.Hour, time.Hour, mode)
		n := nextOf(t, s, now)
		if n == nil || n.Estimate || n.Warmup {
			t.Fatalf("%s: next %+v", mode.Kind, n)
		}
		lc := s.state.lifeCount(now)
		if n.Minute != lc+1 || n.InSecs <= 0 || n.InSecs > 61 {
			t.Errorf("%s: minute %d (now %d), in %.1f s", mode.Kind, n.Minute, lc, n.InSecs)
		}
		// Advance the sensor to that minute and compare.
		s.Tick(now.Add(time.Duration(n.InSecs*float64(time.Second)) + time.Second))
		if got := s.Reading(n.Minute); int(got) != n.MgDl {
			t.Errorf("%s: predicted %d, the sensor made %d", mode.Kind, n.MgDl, got)
		}
	}
}

// For walk the value is an estimate, and asking for it must not disturb the
// random sequence the sensor then produces.
func TestNextReadingWalk(t *testing.T) {
	s, now := readingsSensor(t, 3*time.Hour, time.Hour, GlucoseMode{Kind: "walk", Value: 140})
	n := nextOf(t, s, now)
	if n == nil || !n.Estimate || n.NoiseSD <= 0 {
		t.Fatalf("next %+v", n)
	}
	before := len(s.state.Readings)
	for i := 0; i < 100; i++ {
		s.Status()
		s.History(60, 0, now)
	}
	if len(s.state.Readings) != before {
		t.Fatalf("looking at the next reading made readings: %d -> %d", before, len(s.state.Readings))
	}
	// The estimate is the model's mean: the real value lands near it.
	s.Tick(now.Add(time.Duration(n.InSecs*float64(time.Second)) + time.Second))
	if d := math.Abs(float64(s.Reading(n.Minute)) - float64(n.MgDl)); d > 6*walkNoiseSD {
		t.Errorf("estimate %d, actual %d", n.MgDl, s.Reading(n.Minute))
	}
}

func TestNextReadingEdges(t *testing.T) {
	// Not activated, terminated: no next reading.
	s, now := readingsSensor(t, 0, time.Hour, GlucoseMode{Kind: "fixed", Value: 100})
	if nextOf(t, s, now) != nil {
		t.Error("a next reading for a sensor in its applicator")
	}
	if _, ok := s.Status()["trend"]; ok {
		t.Error("a trend for a sensor in its applicator")
	}
	s, now = readingsSensor(t, 3*time.Hour, time.Hour, GlucoseMode{Kind: "fixed", Value: 100})
	s.Terminate(now)
	if nextOf(t, s, now) != nil {
		t.Error("a next reading for a terminated sensor")
	}
	// In warmup: no reading yet.
	s, now = readingsSensor(t, 10*time.Minute, time.Hour, GlucoseMode{Kind: "fixed", Value: 100})
	if n := nextOf(t, s, now); n == nil || !n.Warmup || n.MgDl != 0 {
		t.Errorf("warmup: %+v", n)
	}
}

func TestTrendAndHistory(t *testing.T) {
	s, now := readingsSensor(t, 3*time.Hour, time.Hour, GlucoseMode{Kind: "sine", Value: 120, Amplitude: 40, PeriodMinutes: 240})
	h := s.History(60, 0, now)
	if len(h.Values) != 60 || h.Minute-h.FirstMinute != 59 || h.Next == nil || h.Trend == nil {
		t.Fatalf("history: %d values, minutes %d to %d, next %v, trend %v", len(h.Values), h.FirstMinute, h.Minute, h.Next, h.Trend)
	}
	last := int(h.Values[59])
	if h.Trend.Delta1 == nil || *h.Trend.Delta1 != last-int(h.Values[58]) || *h.Trend.Delta5 != last-int(h.Values[54]) {
		t.Errorf("trend %+v against %v", h.Trend, h.Values[50:])
	}
	// During warmup the early minutes are 0 and trend deltas are missing.
	s, now = readingsSensor(t, 62*time.Minute, time.Hour, GlucoseMode{Kind: "fixed", Value: 100})
	h = s.History(60, 0, now)
	if h.Trend == nil || h.Trend.Delta1 == nil || h.Trend.Delta5 != nil {
		t.Errorf("trend near the end of warmup: %+v", h.Trend)
	}
	// A short sensor history is shorter than asked for.
	s, now = readingsSensor(t, 5*time.Minute, 0, GlucoseMode{Kind: "fixed", Value: 100})
	if h := s.History(60, 0, now); len(h.Values) != 6 || h.FirstMinute != 0 {
		t.Errorf("young sensor: %d values from minute %d", len(h.Values), h.FirstMinute)
	}
}

func TestEndWarmup(t *testing.T) {
	s, now := readingsSensor(t, 10*time.Minute+30*time.Second, time.Hour, GlucoseMode{Kind: "fixed", Value: 100})
	if f := s.state.framState(now); f != framWarmup {
		t.Fatalf("framState %d, want warmup", f)
	}
	if err := s.EndWarmup(now); err != nil {
		t.Fatal(err)
	}
	// Minute 10 stays without a reading; minute 11 is the first.
	if n := nextOf(t, s, now); n == nil || n.Minute != 11 || n.Warmup || n.MgDl != 100 {
		t.Fatalf("next after ending warmup: %+v", n)
	}
	later := now.Add(time.Minute)
	if g := s.Status(); s.Tick(later) != 11 || s.Reading(10) != 0 || s.Reading(11) != 100 || s.state.framState(later) != framReady {
		t.Fatalf("a minute later: readings %d, %d, framState %d (%v)", s.Reading(10), s.Reading(11), s.state.framState(later), g["framState"])
	}
	if s.EndWarmup(now) == nil || s.EndWarmup(later) == nil {
		t.Error("ended warmup twice")
	}

	ended := false
	for _, e := range func() []Event { ev, _ := s.Events().Since(0); return ev }() {
		ended = ended || e.Kind == "warmup-ended"
	}
	if !ended {
		t.Error("no warmup-ended event")
	}

	if s, now := readingsSensor(t, 0, time.Hour, GlucoseMode{Kind: "sine", Value: 120}); s.EndWarmup(now) == nil {
		t.Error("ended the warmup of a sensor in its applicator")
	}
	if s, now := readingsSensor(t, 3*time.Hour, time.Hour, GlucoseMode{Kind: "sine", Value: 120}); s.EndWarmup(now) == nil {
		t.Error("ended the warmup of a ready sensor")
	}
}

// The coming readings of fixed and sine are exactly what the sensor then
// makes, minute by minute.
func TestUpcomingDeterministic(t *testing.T) {
	s, now := readingsSensor(t, 3*time.Hour, time.Hour, GlucoseMode{Kind: "sine", Value: 120, Amplitude: 40, PeriodMinutes: 90})
	h := s.History(60, 15, now)
	if len(h.Ahead) != 15 || h.WearEnds != 0 {
		t.Fatalf("ahead %d, wear ends %d", len(h.Ahead), h.WearEnds)
	}
	if h.Ahead[0] != *h.Next {
		t.Errorf("ahead[0] %+v, next %+v", h.Ahead[0], *h.Next)
	}
	s.Tick(now.Add(16 * time.Minute))
	for i, r := range h.Ahead {
		if r.Minute != h.Minute+i+1 || r.Estimate {
			t.Fatalf("ahead[%d] %+v", i, r)
		}
		if got := s.Reading(r.Minute); int(got) != r.MgDl {
			t.Errorf("minute %d: predicted %d, the sensor made %d", r.Minute, r.MgDl, got)
		}
	}
}

// For walk they are the mean path, drifting toward the mode's value, with a
// spread that grows; asking for them does not draw from the sequence.
func TestUpcomingWalk(t *testing.T) {
	s, now := readingsSensor(t, 3*time.Hour, time.Hour, GlucoseMode{Kind: "walk", Value: 140})
	s.state.fill(s.state.lifeCount(now))
	s.state.Readings[len(s.state.Readings)-1] = 200
	before := len(s.state.Readings)
	h := s.History(60, 15, now)
	if len(s.state.Readings) != before {
		t.Fatalf("readings %d, want %d", len(s.state.Readings), before)
	}
	last, sd := 200, 0.0
	for i, r := range h.Ahead {
		if !r.Estimate || r.MgDl >= last || r.MgDl < 140 || r.NoiseSD <= sd {
			t.Fatalf("ahead[%d] %+v after %d mg/dL, sd %.2f", i, r, last, sd)
		}
		last, sd = r.MgDl, r.NoiseSD
	}
	if math.Abs(h.Ahead[0].NoiseSD-walkNoiseSD) > 0.01 {
		t.Errorf("first sd %.2f, want %.2f", h.Ahead[0].NoiseSD, walkNoiseSD)
	}
}

// The window stops where the wear ends, and minutes still in warmup have no
// reading.
func TestUpcomingWarmupAndWearEnd(t *testing.T) {
	s, now := readingsSensor(t, 50*time.Minute, time.Hour, GlucoseMode{Kind: "fixed", Value: 100})
	h := s.History(60, 15, now)
	if len(h.Ahead) != 15 {
		t.Fatalf("ahead %d", len(h.Ahead))
	}
	for _, r := range h.Ahead {
		if warm := r.Minute < 60; r.Warmup != warm || (r.MgDl == 0) != warm {
			t.Errorf("minute %d: %+v", r.Minute, r)
		}
	}
	s, now = readingsSensor(t, 15*24*time.Hour-5*time.Minute-30*time.Second, time.Hour, GlucoseMode{Kind: "fixed", Value: 100})
	h = s.History(60, 15, now)
	if h.WearEnds != s.state.WearMinutes || len(h.Ahead) != s.state.WearMinutes-h.Minute-1 {
		t.Errorf("wear ends %d (want %d), ahead %d at minute %d", h.WearEnds, s.state.WearMinutes, len(h.Ahead), h.Minute)
	}
}

// Each reading's record holds the raw value the app turns back into that
// reading, and decodes as the packet's records do.
func TestHistoryRecords(t *testing.T) {
	s, now := readingsSensor(t, 3*time.Hour, time.Hour, GlucoseMode{Kind: "sine", Value: 120, Amplitude: 40, PeriodMinutes: 90})
	h := s.History(60, 15, now)
	if len(h.Records) != len(h.Values) || len(h.AheadRecords) != len(h.Ahead) {
		t.Fatalf("records %d for %d values, %d for %d ahead", len(h.Records), len(h.Values), len(h.AheadRecords), len(h.Ahead))
	}
	c := s.state.Calibration
	for i, v := range h.Values {
		d := h.Records[i]
		if int(math.Round(d.AppMgDl)) != int(v) || d.Raw != int(c.rawForGlucose(v)) {
			t.Errorf("minute %d: %d mg/dL, record %+v", h.FirstMinute+i, v, d)
		}
		var b [bleRecordSize]byte
		putBLERecord(b[:], uint16(d.Raw), c.RawTemperature, c.RawTemperatureAdjustment)
		if readBits(b[:], 0, 0, 0xe) != d.Raw {
			t.Errorf("minute %d: record does not hold raw %d", h.FirstMinute+i, d.Raw)
		}
	}
	if w := s.History(60, 0, now.Add(-3*time.Hour+30*time.Minute)); len(w.Records) == 0 || w.Records[0].Raw != 0 || w.Records[0].AppMgDl != 0 {
		t.Errorf("warmup record %+v", w.Records)
	}
}
