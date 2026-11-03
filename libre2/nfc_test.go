package libre2

import (
	"bytes"
	"encoding/hex"
	"errors"
	"strings"
	"testing"
	"time"
)

// pairingInfo is what SensorPairingInfo carries out of a scan.
type pairingInfo struct {
	uid, patchInfo, fram []byte // fram decrypted
	streamingEnabled     bool
	macAddress           string
}

// pair runs LibreTransmitter's pairing scan against s, step for step as
// SimulatedSensorPairingService.scan (and SensorPairingService) does it, with
// the same checks.
func pair(t *testing.T, s *Sensor, now time.Time) (*pairingInfo, error) {
	t.Helper()
	id := s.NFCIdentifier()
	sensorUID := make([]byte, len(id))
	for i := range id {
		sensorUID[i] = id[len(id)-1-i]
	}
	// getSystemInfo: nothing to ask the model; the API answers it.
	patchInfo, err := s.NFCCustom(0xA1, nil, now)
	if err != nil {
		return nil, err
	}
	var fram []byte
	for first := 0; first < 43; first += 3 {
		count := 3
		if 43-first < count {
			count = 43 - first
		}
		blocks, err := s.readBlocks(first, count, now)
		if err != nil {
			return nil, err
		}
		for _, b := range blocks {
			fram = append(fram, b...)
		}
	}
	if len(patchInfo) < 6 {
		t.Fatal("noPatchInfo")
	}
	code, params := EnableStreamingCommand(sensorUID, patchInfo, 42)
	resp, err := s.NFCCustom(code, params, now)
	if err != nil {
		return nil, err
	}
	info := &pairingInfo{uid: sensorUID, patchInfo: patchInfo}
	if len(resp) == 6 {
		info.streamingEnabled = true
		rev := make([]byte, 6)
		for i := range resp {
			rev[i] = resp[5-i]
		}
		info.macAddress = strings.ToUpper(hex.EncodeToString(rev))
	}
	if len(sensorUID) != 8 || len(patchInfo) != 6 || len(fram) != 344 {
		t.Fatalf("noSensorData: uid %d, patch info %d, fram %d bytes", len(sensorUID), len(patchInfo), len(fram))
	}
	switch patchInfo[0] { // SensorType(patchInfo:) == .libre2
	case 0xC5, 0x9D, 0xC6, 0x7F:
	default:
		t.Fatalf("wrongSensorType: patch info %x", patchInfo)
	}
	info.fram = DecryptFRAM(sensorUID, patchInfo, fram)
	return info, nil
}

func testSensor(t *testing.T, now time.Time, age time.Duration) *Sensor {
	t.Helper()
	return NewSensor(testState(t, now, age), t.TempDir()+"/state.json")
}

func TestPairing(t *testing.T) {
	now := time.Now()
	s := testSensor(t, now, 3*time.Hour)
	info, err := pair(t, s, now)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(info.uid[5:], []byte{0xa4, 0x07, 0xe0}) {
		t.Errorf("sensorUID %x does not end a4 07 e0", info.uid)
	}
	if info.patchInfo[2]>>4 != 3 {
		t.Errorf("patch info %x: family %d, want 3 (Libre 2)", info.patchInfo, info.patchInfo[2]>>4)
	}
	if h, b, f := checkFRAMCRCs(t, info.fram); !h || !b || !f {
		t.Fatalf("CRC header=%v body=%v footer=%v", h, b, f)
	}
	d := sensorData{info.fram}
	if d.calibration() != calibrationData(framTemplate) {
		t.Errorf("calibration %+v", d.calibration())
	}
	if d.maxMinutesWearTime() != 21600 || d.minutesSinceStart() != 180 || d.header()[4] != framReady {
		t.Errorf("wear %d, minutes %d, state %02x", d.maxMinutesWearTime(), d.minutesSinceStart(), d.header()[4])
	}
	if !info.streamingEnabled || info.macAddress != s.AdvertisedName() {
		t.Errorf("macAddress %q, want %q", info.macAddress, s.AdvertisedName())
	}

	// The code was kept and saved, for the BLE unlock.
	saved, err := LoadState(s.path)
	if err != nil {
		t.Fatal(err)
	}
	if saved.StreamingCode == nil || *saved.StreamingCode != 42 {
		t.Errorf("saved streaming code %v, want 42", saved.StreamingCode)
	}
}

func nfcCode(err error) int {
	var e *NFCError
	if !errors.As(err, &e) {
		return -1
	}
	return int(e.Code)
}

func TestEnableStreamingWrongD(t *testing.T) {
	now := time.Now()
	s := testSensor(t, now, 3*time.Hour)
	_, params := EnableStreamingCommand(s.state.uid(), s.state.patchInfo(), 42)
	for _, c := range []struct {
		name   string
		params []byte
	}{
		{"flipped d", append(append([]byte(nil), params[:8]...), params[8]^1)},
		{"other code, same d", append([]byte{0x1e, 43, 0, 0, 0}, params[5:]...)},
		{"another sensor's d", enableStreamingParams(example2UID, s.state.patchInfo(), 42)},
	} {
		resp, err := s.NFCCustom(0xA1, c.params, now)
		if nfcCode(err) != 0x02 {
			t.Errorf("%s: got %x, %v; want NFC error 0x02", c.name, resp, err)
		}
	}
	if s.state.StreamingCode != nil {
		t.Error("a refused command enabled streaming")
	}
}

func TestEnableStreamingNeedsARunningSensor(t *testing.T) {
	now := time.Now()
	for _, c := range []struct {
		name string
		age  time.Duration
		end  bool
	}{
		{"not activated", -1, false},
		{"expired", 15 * 24 * time.Hour, false},
		{"terminated", 2 * time.Hour, true},
	} {
		s := testSensor(t, now, c.age)
		if c.end {
			s.Terminate(now)
		}
		code, params := EnableStreamingCommand(s.state.uid(), s.state.patchInfo(), 42)
		if resp, err := s.NFCCustom(code, params, now); nfcCode(err) != 0xB0 {
			t.Errorf("%s: got %x, %v; want NFC error 0xB0", c.name, resp, err)
		}
		if s.state.StreamingCode != nil {
			t.Errorf("%s: streaming enabled", c.name)
		}
	}

	// Activated, even in warmup, it works.
	s := testSensor(t, now, -1)
	if err := s.Activate(now); err != nil {
		t.Fatal(err)
	}
	if err := s.Activate(now); err == nil {
		t.Error("activated twice")
	}
	info, err := pair(t, s, now.Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if d := (sensorData{info.fram}); d.header()[4] != framWarmup || d.minutesSinceStart() != 1 {
		t.Errorf("state %02x, minutes %d", d.header()[4], d.minutesSinceStart())
	}
	if info.macAddress != s.AdvertisedName() {
		t.Errorf("macAddress %q, want %q", info.macAddress, s.AdvertisedName())
	}
}

func TestNFCErrors(t *testing.T) {
	now := time.Now()
	s := testSensor(t, now, time.Hour)
	for _, r := range [][2]int{{-1, 1}, {42, 2}, {0, 0}, {43, 1}, {0, 44}} {
		if _, err := s.NFCReadBlocks(r[0], r[1]); nfcCode(err) != 0x10 {
			t.Errorf("readBlocks %d+%d: %v, want NFC error 0x10", r[0], r[1], err)
		}
	}
	if b, err := s.NFCReadBlocks(42, 1); err != nil || len(b) != 1 || len(b[0]) != 8 {
		t.Errorf("readBlocks 42+1: %x, %v", b, err)
	}
	for _, c := range []struct {
		code   byte
		params []byte
	}{
		{0xA2, nil},
		{0xA1, []byte{0x1f}},
		{0xA1, []byte{0x1e, 42, 0, 0, 0}},
		{0xB1, enableStreamingParams(s.state.uid(), s.state.patchInfo(), 42)},
	} {
		if _, err := s.NFCCustom(c.code, c.params, now); nfcCode(err) != 0x01 {
			t.Errorf("custom %02x %x: %v, want NFC error 0x01", c.code, c.params, err)
		}
	}
}

// A scan that straddles a minute keeps reading the image it started with.
func TestReadBlocksSnapshot(t *testing.T) {
	now := time.Now()
	s := testSensor(t, now, 2*time.Hour+59*time.Second+500*time.Millisecond)
	var fram []byte
	for first := 0; first < 43; first += 3 {
		count := 3
		if 43-first < count {
			count = 43 - first
		}
		blocks, err := s.readBlocks(first, count, now.Add(time.Duration(first)*50*time.Millisecond))
		if err != nil {
			t.Fatal(err)
		}
		for _, b := range blocks {
			fram = append(fram, b...)
		}
	}
	plain := DecryptFRAM(s.state.uid(), s.state.patchInfo(), fram)
	if h, b, f := checkFRAMCRCs(t, plain); !h || !b || !f {
		t.Fatalf("CRC header=%v body=%v footer=%v", h, b, f)
	}
	if m := (sensorData{plain}).minutesSinceStart(); m != 120 {
		t.Errorf("minutes %d, want 120 (the minute the scan started in)", m)
	}
	// The next scan sees the new minute.
	blocks, _ := s.readBlocks(0, 43, now.Add(3*time.Second))
	fram = fram[:0]
	for _, b := range blocks {
		fram = append(fram, b...)
	}
	if m := (sensorData{DecryptFRAM(s.state.uid(), s.state.patchInfo(), fram)}).minutesSinceStart(); m != 121 {
		t.Errorf("next scan: minutes %d, want 121", m)
	}
}
