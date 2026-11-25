package libre2

import (
	"encoding/json"
	"math"
	"testing"
	"time"
)

// rawTemperatureFor inverts the thermistor model to the record's resolution,
// and keeps the template's fixed temperature as it was.
func TestRawTemperatureFor(t *testing.T) {
	st, err := NewState(15, time.Hour, 0)
	if err != nil {
		t.Fatal(err)
	}
	c := st.Calibration
	if got := c.rawTemperatureFor(c.temperature()); got != defaultRawTemperature {
		t.Errorf("default %.2f °C → raw %d, want %d", c.temperature(), got, defaultRawTemperature)
	}
	for celsius := minCelsius; celsius <= maxCelsius; celsius += 0.5 {
		raw := c.rawTemperatureFor(celsius)
		if raw%4 != 0 || math.Abs(c.temperatureFor(raw, c.RawTemperatureAdjustment)-celsius) > 0.05 {
			t.Errorf("%.1f °C → raw %d → %.3f °C", celsius, raw, c.temperatureFor(raw, c.RawTemperatureAdjustment))
		}
	}
}

// appAt is what LibreTransmitter computes for minute's record: its raw glucose
// at its own thermistor temperature.
func appAt(s *Sensor, minute int) float64 {
	raw, rawT, adj := s.state.record(minute)
	c := s.state.Calibration
	return c.mgdlForRawAt(int(raw), c.temperatureFor(rawT, adj))
}

// Without a lag, any skin temperature round-trips: the app gets each reading
// back, and the records carry the new temperature from the current minute on.
func TestTemperatureWithoutLag(t *testing.T) {
	s, now := readingsSensor(t, 3*time.Hour, time.Hour, GlucoseMode{Kind: "fixed", Value: 100})
	lc := s.state.lifeCount(now)
	if err := s.SetTemperature(TemperatureMode{Celsius: 27}, now); err != nil {
		t.Fatal(err)
	}
	s.Tick(now.Add(10 * time.Minute))
	c := s.state.Calibration
	if _, rawT, _ := s.state.record(lc - 1); rawT != c.RawTemperature {
		t.Errorf("the previous minute changed temperature: raw %d", rawT)
	}
	for m := lc; m <= lc+10; m++ {
		_, rawT, adj := s.state.record(m)
		if got := c.temperatureFor(rawT, adj); math.Abs(got-27) > 0.05 {
			t.Errorf("minute %d: %.2f °C", m, got)
		}
		if got := appAt(s, m); math.Round(got) != 100 {
			t.Errorf("minute %d: the app gets %.2f, want 100", m, got)
		}
	}
}

// With a lag, a skin step reaches the raw glucose gradually: the app, which
// corrects with the skin temperature, is off at first and converges.
func TestTemperatureWithTissueLag(t *testing.T) {
	s, now := readingsSensor(t, 3*time.Hour, time.Hour, GlucoseMode{Kind: "fixed", Value: 100})
	lc := s.state.lifeCount(now)
	if err := s.SetTemperature(TemperatureMode{Celsius: 27, TissueLagMinutes: 5}, now); err != nil {
		t.Fatal(err)
	}
	s.Tick(now.Add(60 * time.Minute))
	first, after5, after40 := appAt(s, lc), appAt(s, lc+5), appAt(s, lc+40)
	// Colder skin means more correction; the tissue is still warm, so the app
	// over-reads at first.
	if first < 110 || !(after5 < first) || math.Abs(after40-100) > 0.5 {
		t.Errorf("app after the step: %.1f, then %.1f at 5 min, %.1f at 40 min", first, after5, after40)
	}
	tissue := s.state.TissueCelsius
	if !(tissue[lc] > 30 && tissue[lc] < tissue[lc-1] && tissue[lc+5] < tissue[lc] && math.Abs(tissue[lc+40]-27) < 0.05) {
		t.Errorf("tissue %.2f, %.2f, %.2f °C", tissue[lc], tissue[lc+5], tissue[lc+40])
	}
}

// Setting the temperature every minute, as a trace replay does, gives each
// minute's record that minute's temperature, also when the minute was already
// made, and the same minute's glucose (not the previous minute's).
func TestTemperatureEveryMinute(t *testing.T) {
	s, now := readingsSensor(t, 3*time.Hour, time.Hour, GlucoseMode{Kind: "fixed", Value: 100})
	c := s.state.Calibration
	s.Tick(now) // the current minute is already made at the old temperature
	lc0 := s.state.lifeCount(now)
	for k := 0; k < 10; k++ {
		at := now.Add(time.Duration(k) * time.Minute)
		if err := s.SetTemperature(TemperatureMode{Celsius: 25 + float64(k)}, at); err != nil {
			t.Fatal(err)
		}
		s.SetGlucoseMode(GlucoseMode{Kind: "fixed", Value: float64(100 + k)})
		s.Tick(at)
	}
	for k := 0; k < 10; k++ {
		m := lc0 + k
		_, rawT, adj := s.state.record(m)
		if got := c.temperatureFor(rawT, adj); math.Abs(got-(25+float64(k))) > 0.05 {
			t.Errorf("minute %d: %.2f °C, want %d", m, got, 25+k)
		}
		if got := s.state.reading(m); got != uint16(100+k) && k > 0 {
			t.Errorf("minute %d: glucose %d, want %d", m, got, 100+k)
		}
		if got := appAt(s, m); math.Abs(got-float64(s.state.reading(m))) > 0.6 {
			t.Errorf("minute %d: the app gets %.2f for %d", m, got, s.state.reading(m))
		}
	}
}

// A state saved before temperatures were kept gets the calibration's
// temperature for its old minutes, and new minutes line up with Readings.
func TestTemperatureOldState(t *testing.T) {
	s, now := readingsSensor(t, 3*time.Hour, time.Hour, GlucoseMode{Kind: "fixed", Value: 100})
	s.state.fill(s.state.lifeCount(now))
	b, _ := json.Marshal(s.state)
	var old map[string]interface{}
	json.Unmarshal(b, &old)
	delete(old, "skinRaw")
	delete(old, "tissueCelsius")
	b, _ = json.Marshal(old)
	var st State
	if err := json.Unmarshal(b, &st); err != nil {
		t.Fatal(err)
	}
	n := len(st.Readings)
	st.fill(n + 4)
	if len(st.SkinRaw) != len(st.Readings) || len(st.TissueCelsius) != len(st.Readings) {
		t.Fatalf("readings %d, skin %d, tissue %d", len(st.Readings), len(st.SkinRaw), len(st.TissueCelsius))
	}
	if raw, rawT, _ := st.record(n - 1); rawT != st.Calibration.RawTemperature || raw != st.Calibration.rawForGlucose(100) {
		t.Errorf("an old minute: raw %d, temperature %d", raw, rawT)
	}
}

// A temperature setting with an adjustment puts that adjustment in the
// records from the current minute on, with the raw temperature that gives the
// set °C at it; earlier minutes keep theirs, and the app gets each reading back.
func TestTemperatureAdjustment(t *testing.T) {
	s, now := readingsSensor(t, 3*time.Hour, time.Hour, GlucoseMode{Kind: "fixed", Value: 100})
	c := s.state.Calibration
	lc := s.state.lifeCount(now)
	s.Tick(now)
	for k, a := range []int{0, -40, 8, 2044} {
		a := a
		at := now.Add(time.Duration(k) * time.Minute)
		if err := s.SetTemperature(TemperatureMode{Celsius: 30, Adjustment: &a}, at); err != nil {
			t.Fatal(err)
		}
		s.Tick(at)
		m := lc + k
		_, rawT, adj := s.state.record(m)
		if adj != a {
			t.Errorf("minute %d: adjustment %d, want %d", m, adj, a)
		}
		if got := c.temperatureFor(rawT, adj); math.Abs(got-30) > 0.05 {
			t.Errorf("minute %d: %.2f °C with adjustment %d", m, got, adj)
		}
		if got := appAt(s, m); math.Round(got) != 100 {
			t.Errorf("minute %d: the app gets %.2f, want 100", m, got)
		}
		// The packet and the FRAM carry it.
		if r := parseMeasurement(recordBytes(s, m)); r.RawTemperatureAdjustment != a {
			t.Errorf("minute %d: the encoded record has adjustment %d, want %d", m, r.RawTemperatureAdjustment, a)
		}
	}
	if _, _, adj := s.state.record(lc - 1); adj != c.RawTemperatureAdjustment {
		t.Errorf("the previous minute changed adjustment: %d", adj)
	}
	// Without an adjustment the setting goes back to the calibration's.
	s.SetTemperature(TemperatureMode{Celsius: 30}, now.Add(4*time.Minute))
	s.Tick(now.Add(4 * time.Minute))
	if _, _, adj := s.state.record(lc + 4); adj != c.RawTemperatureAdjustment {
		t.Errorf("no adjustment set: %d, want the calibration's %d", adj, c.RawTemperatureAdjustment)
	}
}

// recordBytes is minute m's record as putMeasurement encodes it.
func recordBytes(s *Sensor, m int) []byte {
	raw, rawT, adj := s.state.record(m)
	b := make([]byte, measurementSize)
	putMeasurement(b, raw, rawT, adj)
	return b
}

// A state saved before adjustments were kept gets the calibration's for its
// old minutes, and new minutes line up with Readings.
func TestTemperatureAdjustmentOldState(t *testing.T) {
	s, now := readingsSensor(t, 3*time.Hour, time.Hour, GlucoseMode{Kind: "fixed", Value: 100})
	s.state.fill(s.state.lifeCount(now))
	s.state.SkinAdjustment = nil
	n := len(s.state.Readings)
	s.state.fill(n + 4)
	if len(s.state.SkinAdjustment) != len(s.state.Readings) {
		t.Fatalf("readings %d, adjustments %d", len(s.state.Readings), len(s.state.SkinAdjustment))
	}
	if _, _, adj := s.state.record(n - 1); adj != s.state.Calibration.RawTemperatureAdjustment {
		t.Errorf("an old minute: adjustment %d", adj)
	}
}

func TestSetTemperatureRange(t *testing.T) {
	s, _ := readingsSensor(t, 3*time.Hour, time.Hour, GlucoseMode{Kind: "fixed", Value: 100})
	odd, big := 6, 2048
	for _, m := range []TemperatureMode{{Celsius: 19}, {Celsius: 43}, {Celsius: 30, TissueLagMinutes: -1}, {Celsius: 30, TissueLagMinutes: 61},
		{Celsius: 30, Adjustment: &odd}, {Celsius: 30, Adjustment: &big}} {
		if s.SetTemperature(m, time.Now()) == nil {
			t.Errorf("%+v accepted", m)
		}
	}
}
