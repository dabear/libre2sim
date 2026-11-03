package libre2

import (
	"bytes"
	"encoding/binary"
	"math"
	"testing"
	"time"
)

// testState is a 15-day sensor with a 60-minute warmup, activated age before
// now (not activated if age < 0).
func testState(t *testing.T, now time.Time, age time.Duration) *State {
	t.Helper()
	s, err := NewState(15, time.Hour, 0)
	if err != nil {
		t.Fatal(err)
	}
	if age >= 0 {
		at := now.Add(-age)
		s.Activated = &at
	}
	return s
}

// sensorData is the parts of SensorData a test reads, computed as the Swift
// computes them.
type sensorData struct{ b []byte }

func (d sensorData) header() []byte           { return d.b[headerStart:bodyStart] }
func (d sensorData) body() []byte             { return d.b[bodyStart:footerStart] }
func (d sensorData) footer() []byte           { return d.b[footerStart:] }
func (d sensorData) minutesSinceStart() int   { return int(d.body()[293])<<8 + int(d.body()[292]) }
func (d sensorData) maxMinutesWearTime() int  { return int(d.footer()[7])<<8 + int(d.footer()[6]) }
func (d sensorData) nextTrendBlock() int      { return int(d.body()[2]) }
func (d sensorData) nextHistoryBlock() int    { return int(d.body()[3]) }
func (d sensorData) calibration() Calibration { return calibrationData(d.b) }

// trend is SensorData.trendMeasurements: newest first.
func (d sensorData) trend() []measurement {
	var out []measurement
	for k := 0; k <= 15; k++ {
		index := 4 + (d.nextTrendBlock()-1-k)*6
		if index < 4 {
			index += 96
		}
		out = append(out, parseMeasurement(d.body()[index:index+6]))
	}
	return out
}

// history is SensorData.historyMeasurements: newest first.
func (d sensorData) history() []measurement {
	var out []measurement
	for k := 0; k <= 31; k++ {
		index := 100 + (d.nextHistoryBlock()-1-k)*6
		if index < 100 {
			index += 192
		}
		out = append(out, parseMeasurement(d.body()[index:index+6]))
	}
	return out
}

func TestFRAMTemplateIsExample2(t *testing.T) {
	if !bytes.Equal(framTemplate, cryptFRAM(example2UID, example2Info, example2FRAM)) {
		t.Fatal("framTemplate is not Libre2.Example2 decrypted")
	}
	c := calibrationData(framTemplate)
	want := Calibration{I1: 0, I2: 898, I3: 24, I4: 6819, I5: 8904, I6: 6192}
	if c != want {
		t.Fatalf("Example2 calibration %+v, want %+v", c, want)
	}
}

// The default temperature fields are BLEExample's first record, read with
// PreLibre2.parseBLEData's bit layout.
func TestDefaultTemperatureIsBLEExample(t *testing.T) {
	pt, err := decryptPacket(bleUID, blePacket)
	if err != nil {
		t.Fatal(err)
	}
	temp := readBits(pt, 0, 0xe, 0xc) << 2
	adj := readBits(pt, 0, 0x1a, 0x5) << 2
	if readBits(pt, 0, 0x1f, 1) != 0 {
		adj = -adj
	}
	if temp != defaultRawTemperature || adj != defaultRawTemperatureAdjustment {
		t.Fatalf("BLEExample record 0: temperature %d, adjustment %d; defaults %d, %d",
			temp, adj, defaultRawTemperature, defaultRawTemperatureAdjustment)
	}
}

func TestPlainFRAM(t *testing.T) {
	now := time.Now()
	s := testState(t, now, 3*time.Hour+20*time.Second)
	lc := s.lifeCount(now)

	plain := s.plainFRAM(now)
	if got := cryptFRAM(s.uid(), s.patchInfo(), s.encryptedFRAM(now)); !bytes.Equal(got, plain) {
		t.Fatal("decrypting encryptedFRAM does not give plainFRAM")
	}
	if h, b, f := checkFRAMCRCs(t, plain); !h || !b || !f {
		t.Fatalf("CRC header=%v body=%v footer=%v", h, b, f)
	}

	d := sensorData{plain}
	want := calibrationData(framTemplate)
	if got := d.calibration(); got != want {
		t.Errorf("calibrationData %+v, want Example2's %+v", got, want)
	}
	stateCal := s.Calibration
	stateCal.RawTemperature, stateCal.RawTemperatureAdjustment = 0, 0
	if stateCal != want {
		t.Errorf("State.Calibration %+v, want Example2's %+v", s.Calibration, want)
	}
	if got := d.maxMinutesWearTime(); got != s.WearMinutes || got != 21600 {
		t.Errorf("maxMinutesWearTime %d, want %d", got, s.WearMinutes)
	}
	if got := d.minutesSinceStart(); got != lc || got != 180 {
		t.Errorf("minutesSinceStart %d, want life count %d", got, lc)
	}
	if got := d.header()[4]; got != framReady {
		t.Errorf("state byte %02x, want %02x", got, framReady)
	}
	// Bytes nobody reads are the template's: the header after the state
	// byte and the footer after the wear time.
	if !bytes.Equal(d.header()[5:], framTemplate[5:24]) || !bytes.Equal(d.footer()[8:], framTemplate[328:]) {
		t.Error("plainFRAM changed template bytes outside the fields it owns")
	}

	// Trend: the newest record is this minute's reading, then one a minute.
	for k, m := range d.trend() {
		g := s.reading(lc - k)
		if g == 0 {
			t.Fatalf("trend %d: no reading for minute %d", k, lc-k)
		}
		if m.RawTemperature != s.Calibration.RawTemperature || m.RawTemperatureAdjustment != s.Calibration.RawTemperatureAdjustment || m.Error != 0 {
			t.Errorf("trend %d: %+v", k, m)
		}
		if got := math.Round(s.Calibration.mgdlForRaw(m.RawGlucose)); got != float64(g) {
			t.Errorf("trend %d (minute %d): app computes %v mg/dL, sim modelled %d", k, lc-k, got, g)
		}
	}

	// History: SensorData.dateOfMostRecentHistoryValue's "in sync" case,
	// newest at minute lc - delay, then every 15 minutes back to minute 15.
	if want := ((lc - 3) / 15) % 32; d.nextHistoryBlock() != want {
		t.Fatalf("nextHistoryBlock %d, want %d (in sync with minutesSinceStart)", d.nextHistoryBlock(), want)
	}
	newest := lc - ((lc-3)%15 + 3)
	for k, m := range d.history() {
		minute := newest - 15*k
		if minute < 15 {
			if m != (measurement{}) {
				t.Errorf("history %d (minute %d, before the first): %+v, want empty", k, minute, m)
			}
			continue
		}
		g := s.reading(minute)
		if g == 0 { // warmup
			if m.RawGlucose != 0 {
				t.Errorf("history %d (minute %d, warmup): raw %d", k, minute, m.RawGlucose)
			}
			continue
		}
		if got := math.Round(s.Calibration.mgdlForRaw(m.RawGlucose)); got != float64(g) {
			t.Errorf("history %d (minute %d): app computes %v mg/dL, sim modelled %d", k, minute, got, g)
		}
	}
}

func TestFRAMLifecycle(t *testing.T) {
	now := time.Now()
	for _, c := range []struct {
		name    string
		age     time.Duration // < 0: not activated
		end     bool          // terminated 10 minutes ago
		state   byte
		minutes int
	}{
		{"not activated", -1, false, framNotActivated, 0},
		{"warmup", 30 * time.Minute, false, framWarmup, 30},
		{"ready", 61 * time.Minute, false, framReady, 61},
		{"ready, last minute", 15*24*time.Hour - time.Minute, false, framReady, 21599},
		{"expired", 16 * 24 * time.Hour, false, framShutdown, 21600},
		{"terminated", 5 * time.Hour, true, framShutdown, 290},
	} {
		t.Run(c.name, func(t *testing.T) {
			s := testState(t, now, c.age)
			if c.end {
				s.Terminated = &Termination{LifeCount: uint16(s.lifeCount(now.Add(-10 * time.Minute)))}
			}
			plain := cryptFRAM(s.uid(), s.patchInfo(), s.encryptedFRAM(now))
			if h, b, f := checkFRAMCRCs(t, plain); !h || !b || !f {
				t.Fatalf("CRC header=%v body=%v footer=%v", h, b, f)
			}
			d := sensorData{plain}
			if got := d.header()[4]; got != c.state {
				t.Errorf("state byte %02x, want %02x", got, c.state)
			}
			if got := d.minutesSinceStart(); got != c.minutes {
				t.Errorf("minutesSinceStart %d, want %d", got, c.minutes)
			}
			if got := d.maxMinutesWearTime(); got != 21600 {
				t.Errorf("maxMinutesWearTime %d", got)
			}
			if d.calibration() != calibrationData(framTemplate) {
				t.Errorf("calibration changed: %+v", d.calibration())
			}
			if c.age < 0 && !bytes.Equal(d.body()[4:292], make([]byte, 288)) {
				t.Error("a sensor that never started has records")
			}
		})
	}
}

func TestFRAMWarmupRecords(t *testing.T) {
	now := time.Now()
	s := testState(t, now, 65*time.Minute)
	d := sensorData{s.plainFRAM(now)}
	// Minutes 65…60 have readings, 59…50 are warmup: raw 0.
	for k, m := range d.trend() {
		if minute := 65 - k; (minute >= 60) != (m.RawGlucose != 0) {
			t.Errorf("trend %d (minute %d): raw %d", k, minute, m.RawGlucose)
		}
	}
	if binary.LittleEndian.Uint16(d.body()[292:]) != 65 {
		t.Error("minutes")
	}
}

// The trend ring wraps exactly where SensorData's index arithmetic does, at
// every position of nextTrendBlock.
func TestFRAMTrendRingWraps(t *testing.T) {
	now := time.Now()
	for lc := 80; lc < 80+16; lc++ { // past warmup, so every trend minute has a reading
		s := testState(t, now, time.Duration(lc)*time.Minute)
		s.Glucose = GlucoseMode{Kind: "sine", Value: 150, Amplitude: 100, PeriodMinutes: 20}
		d := sensorData{s.plainFRAM(now)}
		if d.nextTrendBlock() != lc%16 {
			t.Fatalf("lc %d: nextTrendBlock %d", lc, d.nextTrendBlock())
		}
		for k, m := range d.trend() {
			if got := math.Round(s.Calibration.mgdlForRaw(m.RawGlucose)); got != float64(s.reading(lc-k)) {
				t.Fatalf("lc %d, trend %d: %v mg/dL, want %d", lc, k, got, s.reading(lc-k))
			}
		}
	}
}

func TestSerialNumber(t *testing.T) {
	// SensorSerialNumber.swift's worked example: tag UID E0 07 A0 00 00 25
	// 90 5E (sensorUID 5e 90 25 00 00 a0 07 e0), a Libre 1 (family 0).
	if got := SerialNumber(hexBytes("5e90250000a007e0"), 0); got != "0M00009DHCR" {
		t.Errorf("serial %s, want 0M00009DHCR", got)
	}
	s := testState(t, time.Now(), -1)
	if sn := s.Serial(); len(sn) != 11 || sn[0] != '3' {
		t.Errorf("serial %q: want 11 characters starting with 3 (Libre 2 family)", sn)
	}
}

func TestStateSaveLoad(t *testing.T) {
	path := t.TempDir() + "/state.json"
	s := testState(t, time.Now(), time.Hour)
	code := uint32(42)
	s.StreamingCode = &code
	s.fill(70)
	if err := s.Save(path); err != nil {
		t.Fatal(err)
	}
	got, err := LoadState(path)
	if err != nil {
		t.Fatal(err)
	}
	if got.UID != s.UID || got.PatchInfo != s.PatchInfo || got.BLEAddress != s.BLEAddress ||
		*got.StreamingCode != 42 || got.Calibration != s.Calibration || len(got.Readings) != 71 ||
		!got.Activated.Equal(*s.Activated) {
		t.Fatalf("round trip: %+v", got)
	}
	if !bytes.Equal(got.uid()[5:], []byte{0xa4, 0x07, 0xe0}) {
		t.Errorf("uid %s does not end a4 07 e0", got.UID)
	}
	if p := got.patchInfo(); p[0] != 0xc6 || p[1] != 0x09 || p[2] != 0x31 || p[3] != 0x01 {
		t.Errorf("patch info %s", got.PatchInfo)
	}
	if mac := got.macLE(); mac[5]&0xc0 != 0xc0 {
		t.Errorf("BLE address %s is not a random static address", got.BLEAddress)
	}
}
