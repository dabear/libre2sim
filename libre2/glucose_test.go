package libre2

import (
	"math"
	"testing"
	"time"
)

func TestGlucoseTables(t *testing.T) {
	// GlucoseFromRaw.swift's tables have 1023 entries, one per i2 in 1…1023
	// (i2 is 10 bits; i2 = 0 would crash the Swift).
	if len(t1) != 1023 || len(t2) != 1023 {
		t.Fatalf("t1 has %d entries, t2 %d; want 1023", len(t1), len(t2))
	}
	if t1[0] != 0.75 || t2[0] != 0.037744199999999999 || t2[1022] != 0.12834519999999999 {
		t.Errorf("t1[0] %v, t2[0] %v, t2[1022] %v", t1[0], t2[0], t2[1022])
	}
}

func TestSimTemperature(t *testing.T) {
	s, err := NewState(15, time.Hour, 0)
	if err != nil {
		t.Fatal(err)
	}
	// A plausible skin temperature, so the app's temperature compensation
	// stays near 1 rather than being the reason the inversion works.
	if temp := s.Calibration.temperature(); temp < 28 || temp > 36 {
		t.Fatalf("temperature %.2f °C", temp)
	}
}

// What the sim models is what LibreTransmitter shows: for every glucose the
// sim makes up, the app's glucoseValueFromRaw, rounded as
// calibratedGlucose rounds it, gives it back.
func TestRawForGlucoseRoundTrip(t *testing.T) {
	s, err := NewState(15, time.Hour, 0)
	if err != nil {
		t.Fatal(err)
	}
	c := s.Calibration
	prev := uint16(0)
	for g := uint16(40); g <= 400; g++ {
		raw := c.rawForGlucose(g)
		if raw <= prev || raw >= maxRaw {
			t.Fatalf("raw for %d mg/dL is %d (previous %d): want increasing, inside 14 bits", g, raw, prev)
		}
		prev = raw
		if got := math.Round(c.mgdlForRaw(int(raw))); got != float64(g) {
			t.Errorf("%d mg/dL -> raw %d -> %v mg/dL", g, raw, got)
		}
	}
	if raw := c.rawForGlucose(0); raw != 0 {
		t.Errorf("no reading -> raw %d, want 0", raw)
	}
	t.Logf("raw 40 mg/dL = %d, 400 mg/dL = %d", c.rawForGlucose(40), c.rawForGlucose(400))
}

// The round trip holds through the 6-byte record, too, with either sign of
// temperature adjustment.
func TestMeasurementRecord(t *testing.T) {
	for _, c := range []struct{ raw, temp, adj int }{
		{2542, 6336, 8}, {1, 4, -4}, {maxRaw, (1<<12 - 1) << 2, -((1<<9 - 1) << 2)}, {0, 0, 0},
	} {
		b := []byte{0xff, 0xff, 0xff, 0xff, 0xff, 0xff}
		putMeasurement(b, uint16(c.raw), c.temp, c.adj)
		m := parseMeasurement(b)
		if m.RawGlucose != c.raw || m.RawTemperature != c.temp || m.RawTemperatureAdjustment != c.adj || m.Error != 0 {
			t.Errorf("%+v -> %x -> %+v", c, b, m)
		}
	}
	// A record from the real sensor in Example2 reads back the same through
	// the Go port of the bit layout and through writeBits.
	rec := append([]byte(nil), framTemplate[bodyStart+4:bodyStart+10]...)
	m := parseMeasurement(rec)
	b := make([]byte, 6)
	putMeasurement(b, uint16(m.RawGlucose), m.RawTemperature, m.RawTemperatureAdjustment)
	if m.Error != 0 {
		t.Fatalf("Example2 record %x has error bits %x", rec, m.Error)
	}
	if string(b) != string(rec) {
		t.Errorf("Example2 record %x re-encodes as %x", rec, b)
	}
}
