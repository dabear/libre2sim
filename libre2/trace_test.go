package libre2

import (
	"encoding/base64"
	"math"
	"strings"
	"testing"
	"time"
)

// traceFRAM is a synthetic dump: the sim's own FRAM of a sensor worn 10 hours
// with a sine model, and that sensor's state (its readings are what the dump
// should decode to). No real sensor's data.
func traceFRAM(t *testing.T) ([]byte, *State) {
	t.Helper()
	now := time.Now()
	st := testState(t, now, 10*time.Hour)
	st.Glucose = GlucoseMode{Kind: "sine", Value: 150, Amplitude: 60, PeriodMinutes: 200}
	return st.plainFRAM(now), st
}

// The dump decodes to the sensor's own readings at every history and trend
// minute: 32 history values 15 minutes apart, then the last 15 minutes.
func TestDecodeTraceFRAM(t *testing.T) {
	fram, st := traceFRAM(t)
	series, err := DecodeTraceFRAM(fram)
	if err != nil {
		t.Fatal(err)
	}
	lc := 600
	newestHistory := 15 * ((lc - 3) / 15)
	first := newestHistory - 15*31
	if len(series) != lc-first+1 {
		t.Fatalf("%d minutes, want %d", len(series), lc-first+1)
	}
	for k := 0; k < 32; k++ {
		m := newestHistory - 15*k
		if got, want := series[m-first], st.Readings[m]; got != want {
			t.Errorf("history minute %d: %d, the sensor made %d", m, got, want)
		}
	}
	for m := lc - 15; m <= lc; m++ {
		if got, want := series[m-first], st.Readings[m]; got != want {
			t.Errorf("trend minute %d: %d, the sensor made %d", m, got, want)
		}
	}
	// The minutes in between follow the sine to within a few mg/dL.
	for i, v := range series {
		if d := math.Abs(float64(v) - float64(st.Readings[first+i])); d > 4 {
			t.Errorf("minute %d: %d, the sine is %d", first+i, v, st.Readings[first+i])
		}
	}
}

func TestDecodeTraceFRAMRejects(t *testing.T) {
	fram, _ := traceFRAM(t)
	if _, err := DecodeTraceFRAM(fram[:343]); err == nil || !strings.Contains(err.Error(), "344") {
		t.Errorf("343 bytes: %v", err)
	}
	bad := append([]byte(nil), fram...)
	bad[bodyStart+50] ^= 1
	if _, err := DecodeTraceFRAM(bad); err == nil || !strings.Contains(err.Error(), "CRC") {
		t.Errorf("body CRC: %v", err)
	}
	// An encrypted dump fails its CRC too.
	st := testState(t, time.Now(), 10*time.Hour)
	if _, err := DecodeTraceFRAM(st.encryptedFRAM(time.Now())); err == nil {
		t.Error("an encrypted dump decoded")
	}
}

// A trace file: comments, the reference glucose, base64 over several lines.
func TestParseTrace(t *testing.T) {
	fram, _ := traceFRAM(t)
	b64 := base64.StdEncoding.EncodeToString(fram)
	text := "// Synthetic 1, made by libre2sim\n// currentbg: 92\n// another comment\n" + b64[:100] + "\n  " + b64[100:] + "\n"
	tr, err := ParseTrace(text)
	if err != nil {
		t.Fatal(err)
	}
	if tr.ReferenceMgdl == nil || *tr.ReferenceMgdl != 92 || len(tr.Series) < 470 || tr.Label != "Synthetic 1, made by libre2sim" {
		t.Errorf("reference %v, %d minutes, label %q", tr.ReferenceMgdl, len(tr.Series), tr.Label)
	}
	if tr, err := ParseTrace(b64); err != nil || tr.ReferenceMgdl != nil || tr.Label != "" {
		t.Errorf("bare base64: %v, %+v", err, tr)
	}
	for _, bad := range []string{"", "// only a comment", "not base64!", base64.StdEncoding.EncodeToString(fram[:300])} {
		if _, err := ParseTrace(bad); err == nil {
			t.Errorf("%q parsed", bad)
		}
	}
}

// PCHIP passes through every knot and never leaves the range of the two
// knots around a minute, even across a step.
func TestPCHIPFill(t *testing.T) {
	xs := []int{0, 15, 30, 45, 60, 61, 62, 90}
	ys := []float64{100, 100, 180, 182, 120, 121, 119, 119}
	out := pchipFill(xs, ys)
	if len(out) != 91 {
		t.Fatalf("%d values", len(out))
	}
	for k, x := range xs {
		if out[x] != ys[k] {
			t.Errorf("knot %d: %g, want %g", x, out[x], ys[k])
		}
	}
	for k := 0; k+1 < len(xs); k++ {
		lo, hi := math.Min(ys[k], ys[k+1]), math.Max(ys[k], ys[k+1])
		for x := xs[k]; x <= xs[k+1]; x++ {
			if out[x] < lo-1e-9 || out[x] > hi+1e-9 {
				t.Errorf("minute %d: %g outside %g…%g", x, out[x], lo, hi)
			}
			if x > xs[k] && (out[x]-out[x-1])*(ys[k+1]-ys[k]) < -1e-9 {
				t.Errorf("minute %d: not monotone (%g after %g)", x, out[x], out[x-1])
			}
		}
	}
	if two := pchipFill([]int{0, 4}, []float64{100, 120}); len(two) != 5 || two[2] != 110 {
		t.Errorf("two knots: %v", two)
	}
}

// A trace plays from the next minute, loops, and its coming readings are
// exact.
func TestTracePlayback(t *testing.T) {
	s, now := readingsSensor(t, 3*time.Hour, time.Hour, GlucoseMode{Kind: "fixed", Value: 100})
	tr := &Trace{Series: []uint16{}}
	for i := 0; i < 20; i++ {
		tr.Series = append(tr.Series, uint16(80+5*i))
	}
	s.SetGlucoseTrace("synthetic", tr, now)
	lc := s.state.lifeCount(now)
	m := s.state.Glucose
	if m.Kind != "trace" || m.Trace != "synthetic" || m.TraceStartMinute != lc+1 || m.TraceMinutes != 20 {
		t.Fatalf("mode %+v at minute %d", m, lc)
	}
	if got := s.Reading(lc); got != 100 {
		t.Errorf("the current minute changed: %d", got)
	}
	h := s.History(60, 30, now)
	if len(h.Ahead) != 30 {
		t.Fatalf("%d ahead", len(h.Ahead))
	}
	for k, r := range h.Ahead {
		if want := int(tr.Series[k%20]); r.MgDl != want || r.Estimate {
			t.Errorf("ahead %d: %+v, want %d", k, r, want)
		}
	}
	s.Tick(now.Add(31 * time.Minute))
	for _, r := range h.Ahead {
		if got := s.Reading(r.Minute); int(got) != r.MgDl {
			t.Errorf("minute %d: predicted %d, made %d", r.Minute, r.MgDl, got)
		}
	}
	// Saved and restored, it plays on.
	path := t.TempDir() + "/state.json"
	if err := s.state.Save(path); err != nil {
		t.Fatal(err)
	}
	back, err := LoadState(path)
	if err != nil {
		t.Fatal(err)
	}
	if back.Glucose != m || len(back.TraceSeries) != 20 || back.model(lc+1+45) != float64(tr.Series[5]) {
		t.Errorf("restored %+v, %d minutes", back.Glucose, len(back.TraceSeries))
	}
	// Another model drops the trace.
	s.SetGlucoseMode(GlucoseMode{Kind: "fixed", Value: 90})
	if s.state.TraceSeries != nil {
		t.Error("the trace stayed")
	}
}

// Set in warmup, a trace starts at the first reading.
func TestTraceStartsAfterWarmup(t *testing.T) {
	s, now := readingsSensor(t, 10*time.Minute, time.Hour, GlucoseMode{Kind: "fixed", Value: 100})
	s.SetGlucoseTrace("synthetic", &Trace{Series: []uint16{70, 71, 72}}, now)
	if m := s.state.Glucose; m.TraceStartMinute != 60 {
		t.Errorf("starts at %d, want 60", m.TraceStartMinute)
	}
}

// Every step-th minute, ending at the latest, without records.
func TestHistoryEvery(t *testing.T) {
	s, now := readingsSensor(t, 10*time.Hour, time.Hour, GlucoseMode{Kind: "sine", Value: 120, Amplitude: 40, PeriodMinutes: 90})
	h := s.HistoryEvery(1<<16, 30, 15, now)
	if h.Minute != 600 || h.Step != 30 || h.FirstMinute != 0 || len(h.Values) != 21 || h.Records != nil || h.AheadRecords != nil || len(h.Ahead) != 15 {
		t.Fatalf("minute %d step %d first %d, %d values, records %v", h.Minute, h.Step, h.FirstMinute, len(h.Values), h.Records)
	}
	for i, v := range h.Values {
		if want := s.Reading(h.FirstMinute + 30*i); v != want {
			t.Errorf("value %d: %d, want %d", i, v, want)
		}
	}
	if h := s.HistoryEvery(100, 7, 0, now); h.FirstMinute != 600-98 || h.Values[len(h.Values)-1] != s.Reading(600) {
		t.Errorf("first %d", h.FirstMinute)
	}
}
