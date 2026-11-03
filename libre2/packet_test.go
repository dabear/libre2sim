package libre2

import (
	"math"
	"testing"
	"time"
)

// bleState is a worn sensor whose readings are all made up front and differ
// from minute to minute, so a record stamped with the wrong minute shows.
func bleState(t *testing.T, warmup time.Duration) *State {
	t.Helper()
	st, err := NewState(15, warmup, 3*time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i <= st.WearMinutes; i++ {
		g := uint16(0)
		if i >= st.WarmupMinutes {
			g = uint16(40 + (i*37)%361)
		}
		st.Readings = append(st.Readings, g)
	}
	return st
}

// checkPacket decrypts a packet as Libre2DirectTransmitter.handleCompleteMessage
// does and checks every record against the sensor's readings.
func checkPacket(t *testing.T, st *State, pkt []byte, lc int) BLEData {
	t.Helper()
	if len(pkt) != packetSize {
		t.Fatalf("packet is %d bytes", len(pkt))
	}
	pt, err := decryptPacket(st.uid(), pkt)
	if err != nil {
		t.Fatalf("minute %d: %v", lc, err)
	}
	d := ParseBLEData(pt)
	if !d.CRCVerified {
		t.Fatalf("minute %d: parseBLEData says the CRC fails", lc)
	}
	if d.Age != lc {
		t.Fatalf("age %d, want %d", d.Age, lc)
	}
	c := st.Calibration
	check := func(r BLERecord, minute int) {
		t.Helper()
		if r.Minute != minute {
			t.Errorf("minute %d record %d: stamped %d, want %d", lc, r.Index, r.Minute, minute)
		}
		if r.RawTemperature != c.RawTemperature || r.RawTemperatureAdjustment != c.RawTemperatureAdjustment {
			t.Errorf("minute %d record %d: temperature %d %+d, want %d %+d", lc, r.Index,
				r.RawTemperature, r.RawTemperatureAdjustment, c.RawTemperature, c.RawTemperatureAdjustment)
		}
		got := math.Round(c.MgdlForRaw(r.RawGlucose))
		if want := float64(st.reading(minute)); got != want {
			t.Errorf("minute %d record %d (minute %d): %v mg/dL, want %v", lc, r.Index, minute, got, want)
		}
	}
	for _, r := range d.Trend {
		check(r, lc-trendAges[r.Index])
	}
	for _, r := range d.History {
		check(r, ((lc-2)/15)*15-15*(r.Index-7))
	}
	// Every record whose minute has a reading must be there.
	wantTrend, wantHistory := 0, 0
	for _, ago := range trendAges {
		if m := lc - ago; m >= 0 && st.reading(m) != 0 {
			wantTrend++
		}
	}
	for i := 7; i < 10; i++ {
		if m := historyMinute(lc, i); m >= 0 && st.reading(m) != 0 {
			wantHistory++
		}
	}
	if len(d.Trend) != wantTrend || len(d.History) != wantHistory {
		t.Errorf("minute %d: %d trend and %d history records, want %d and %d", lc,
			len(d.Trend), len(d.History), wantTrend, wantHistory)
	}
	return d
}

func TestPacketDecodesToTheModelledGlucose(t *testing.T) {
	st := bleState(t, time.Hour)
	for lc := 0; lc <= st.WearMinutes; lc += 7 {
		n := uint16(lc*131 + 5)
		pkt := encryptPacket(st.uid(), byte(n), byte(n>>8), st.packetPlain(lc))
		d := checkPacket(t, st, pkt, lc)
		if lc >= 92 && (len(d.Trend) != 7 || len(d.History) != 3) {
			t.Fatalf("minute %d: %d trend and %d history records", lc, len(d.Trend), len(d.History))
		}
	}
}

func TestPacketDuringWarmup(t *testing.T) {
	st := bleState(t, time.Hour)
	for _, lc := range []int{0, 1, 2, 17, 59} {
		pt := st.packetPlain(lc)
		for i := 0; i < 10; i++ {
			if raw := readBits(pt, i*4, 0, 0xe); raw != 0 {
				t.Errorf("minute %d record %d: raw %d during warmup", lc, i, raw)
			}
		}
		checkPacket(t, st, encryptPacket(st.uid(), 1, 2, pt), lc)
	}
	// Leaving warmup: only the records at or after minute 60 count.
	d := checkPacket(t, st, encryptPacket(st.uid(), 3, 4, st.packetPlain(64)), 64)
	if len(d.Trend) != 3 || len(d.History) != 1 {
		t.Fatalf("minute 64: %d trend and %d history records, want 3 (64, 62, 60) and 1 (60)", len(d.Trend), len(d.History))
	}
}

// Every glucose the sim models survives the 4-byte record and the app's
// algorithm.
func TestBLERecordRoundTrip(t *testing.T) {
	c := bleState(t, 0).Calibration
	for _, adj := range []int{c.RawTemperatureAdjustment, -c.RawTemperatureAdjustment, 0, 124, -124} {
		for g := uint16(40); g <= 400; g++ {
			pt := make([]byte, packetPlainSz)
			putBLERecord(pt[4:], c.rawForGlucose(g), c.RawTemperature, adj)
			pt[40] = 9 // age: record 1 is minute 7
			crc := libreCRC16(pt[:42])
			pt[42], pt[43] = byte(crc>>8), byte(crc)
			d := ParseBLEData(pt)
			if !d.CRCVerified || len(d.Trend) != 1 {
				t.Fatalf("%d mg/dL: %+v", g, d)
			}
			r := d.Trend[0]
			if r.Index != 1 || r.Minute != 7 || r.RawTemperature != c.RawTemperature || r.RawTemperatureAdjustment != adj {
				t.Fatalf("%d mg/dL, adjustment %d: read back %+v", g, adj, r)
			}
			if adj == c.RawTemperatureAdjustment {
				if got := math.Round(c.MgdlForRaw(r.RawGlucose)); got != float64(g) {
					t.Fatalf("%d mg/dL reads back as %v", g, got)
				}
			}
		}
	}
}

func TestNotifyChunks(t *testing.T) {
	pkt := make([]byte, packetSize)
	for i := range pkt {
		pkt[i] = byte(i)
	}
	chunks := notifyChunks(pkt)
	if len(chunks) != 3 || len(chunks[0]) != 20 || len(chunks[1]) != 20 || len(chunks[2]) != 6 {
		t.Fatalf("chunks of %d, %d, %d", len(chunks[0]), len(chunks[1]), len(chunks[2]))
	}
	var joined []byte
	for _, c := range chunks {
		joined = append(joined, c...)
	}
	for i := range pkt {
		if joined[i] != pkt[i] {
			t.Fatal("chunks do not reassemble")
		}
	}
}

// The packet the sensor sends uses its live readings and a fresh nonce each
// time.
func TestSensorPacket(t *testing.T) {
	st := bleState(t, time.Hour)
	s := NewSensor(st, t.TempDir()+"/state.json")
	a, b := s.packet(180), s.packet(180)
	if a[0] == b[0] && a[1] == b[1] {
		t.Fatal("two packets share a nonce")
	}
	checkPacket(t, st, a, 180)
	checkPacket(t, st, b, 180)
}
