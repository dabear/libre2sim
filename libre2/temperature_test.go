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
// back, and the records carry the new temperature from the next minute on.
func TestTemperatureWithoutLag(t *testing.T) {
	s, now := readingsSensor(t, 3*time.Hour, time.Hour, GlucoseMode{Kind: "fixed", Value: 100})
	lc := s.state.lifeCount(now)
	if err := s.SetTemperature(TemperatureMode{Celsius: 27}, now); err != nil {
		t.Fatal(err)
	}
	s.Tick(now.Add(10 * time.Minute))
	c := s.state.Calibration
	if _, rawT, _ := s.state.record(lc); rawT != c.RawTemperature {
		t.Errorf("the current minute changed temperature: raw %d", rawT)
	}
	for m := lc + 1; m <= lc+10; m++ {
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
	first, after5, after40 := appAt(s, lc+1), appAt(s, lc+5), appAt(s, lc+40)
	// Colder skin means more correction; the tissue is still warm, so the app
	// over-reads at first.
	if first < 110 || !(after5 < first) || math.Abs(after40-100) > 0.5 {
		t.Errorf("app after the step: %.1f, then %.1f at 5 min, %.1f at 40 min", first, after5, after40)
	}
	tissue := s.state.TissueCelsius
	if !(tissue[lc+1] > 30 && tissue[lc+5] < tissue[lc+1] && math.Abs(tissue[lc+40]-27) < 0.05) {
		t.Errorf("tissue %.2f, %.2f, %.2f °C", tissue[lc+1], tissue[lc+5], tissue[lc+40])
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

func TestSetTemperatureRange(t *testing.T) {
	s, _ := readingsSensor(t, 3*time.Hour, time.Hour, GlucoseMode{Kind: "fixed", Value: 100})
	for _, m := range []TemperatureMode{{Celsius: 19}, {Celsius: 43}, {Celsius: 30, TissueLagMinutes: -1}, {Celsius: 30, TissueLagMinutes: 61}} {
		if s.SetTemperature(m, time.Now()) == nil {
			t.Errorf("%+v accepted", m)
		}
	}
}
