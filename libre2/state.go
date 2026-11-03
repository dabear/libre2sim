package libre2

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math"
	mrand "math/rand"
	"os"
	"strings"
	"time"
)

// GlucoseMode is how readings are made up: "sine" around Value, "fixed" at
// Value, or "walk", a mean-reverting random walk around Value.
type GlucoseMode struct {
	Kind          string  `json:"kind"`
	Value         float64 `json:"value"`
	Amplitude     float64 `json:"amplitude,omitempty"`
	PeriodMinutes float64 `json:"periodMinutes,omitempty"`
}

func (m GlucoseMode) String() string { return fmt.Sprintf("%s %.0f", m.Kind, m.Value) }

// Termination is a sensor that has ended early, at LifeCount: its FRAM says
// shut down (0x05) and it refuses to enable streaming.
type Termination struct {
	LifeCount uint16 `json:"lifeCount"`
}

// State is everything about a sensor that outlives the emulator, saved as
// JSON.
type State struct {
	// UID is the sensor's 8-byte NFC UID in LibreTransmitter's sensorUID
	// order (the tag identifier reversed), so it ends a4 07 e0. It keys all
	// of the sensor's crypto.
	UID string `json:"uid"`
	// PatchInfo is what NFC command 0xA1 returns: 6 bytes. Bytes 4…5 key the
	// FRAM and enable-streaming crypto.
	PatchInfo     string `json:"patchInfo"`
	BLEAddress    string `json:"bleAddress"` // display order, e.g. E4:64:47:6E:2C:FE
	WearMinutes   int    `json:"wearMinutes"`
	WarmupMinutes int    `json:"warmupMinutes"`
	// Activated is when the sensor was started; nil while it is still in its
	// applicator. LibreTransmitter cannot start a sensor: use -age or
	// POST /activate.
	Activated *time.Time `json:"activated,omitempty"`
	// StreamingCode is the unlock code of the last accepted enable-streaming
	// command (LibreTransmitter always sends 42); nil until then.
	StreamingCode *uint32 `json:"streamingCode,omitempty"`
	// UnlockCount is the last BLE unlock count the sensor accepted (M4).
	UnlockCount uint16       `json:"unlockCount"`
	Terminated  *Termination `json:"terminated,omitempty"`
	Calibration Calibration  `json:"calibration"`
	Glucose     GlucoseMode  `json:"glucose"`
	// Readings holds the glucose of every minute of wear, by life count; 0
	// where the sensor had none (warmup).
	Readings []uint16 `json:"readings"`
	// Temperature is the skin temperature from the next minute on; nil is the
	// calibration's fixed temperature with no tissue lag.
	Temperature *TemperatureMode `json:"temperature,omitempty"`
	// SkinRaw and TissueCelsius hold, by life count like Readings, the raw
	// temperature each minute's record carries and the tissue temperature its
	// raw glucose was made at. A state saved before they existed has them
	// shorter than Readings; those minutes use the calibration's temperature.
	SkinRaw       []uint16  `json:"skinRaw,omitempty"`
	TissueCelsius []float64 `json:"tissueCelsius,omitempty"`
}

// TemperatureMode is the skin temperature the sensor's thermistor reads, and
// how far the tissue's temperature lags behind it. LibreTransmitter corrects
// glucose for the thermistor's temperature; the glucose signal itself follows
// the tissue. With a lag, a change of skin temperature reaches the raw glucose
// over TissueLagMinutes (an exponential approach), so the correction is wrong
// for a while.
type TemperatureMode struct {
	Celsius          float64 `json:"celsius"`
	TissueLagMinutes float64 `json:"tissueLagMinutes"`
}

// NewState makes a sensor fresh from its applicator: not yet activated. A
// non-zero age instead activates it that long ago, for a sensor that is
// already being worn.
//
// Sim's choices:
//   - UID: 5 random bytes, then a4 07 e0, as every Libre UID in
//     LibreTransmitter ends (07 is Texas Instruments, e0 the ISO 15693 tag
//     prefix, in reverse).
//   - Patch info: C6 09 31 01 and 2 random bytes. LibreTransmitter's
//     SensorType(patchInfo:) makes 9D, C5, C6 and 7F all .libre2, so any of
//     them passes; which one a real Libre 2 Plus reports is still to be
//     checked against a dump. patchInfo[2] >> 4 == 3 is
//     SensorFamily.libre2, which the serial number is prefixed with. The two
//     random bytes are what make each sensor's FRAM keystream its own.
//   - BLE address: a random static address (the top two bits set).
//   - Calibration: Libre2.Example2's, with BLEExample's temperature (see
//     Calibration).
func NewState(wearDays int, warmup, age time.Duration) (*State, error) {
	if wearDays < 1 || wearDays*24*60 > math.MaxUint16 {
		return nil, fmt.Errorf("wear of %d days: want 1 to 45 (FRAM holds it in 16 bits of minutes)", wearDays)
	}
	if warmup < 0 {
		return nil, fmt.Errorf("negative warmup %v", warmup)
	}
	uid := append(random(5), 0xa4, 0x07, 0xe0)
	info := append([]byte{0xc6, 0x09, 0x31, 0x01}, random(2)...)
	addr := random(6)
	addr[0] |= 0xc0
	parts := make([]string, 6)
	for i, b := range addr {
		parts[i] = fmt.Sprintf("%02X", b)
	}
	cal := calibrationData(framTemplate)
	cal.RawTemperature = defaultRawTemperature
	cal.RawTemperatureAdjustment = defaultRawTemperatureAdjustment
	s := &State{
		UID:           hex.EncodeToString(uid),
		PatchInfo:     hex.EncodeToString(info),
		BLEAddress:    strings.Join(parts, ":"),
		WearMinutes:   wearDays * 24 * 60,
		WarmupMinutes: int(warmup / time.Minute),
		Calibration:   cal,
		Glucose:       GlucoseMode{Kind: "sine", Value: 120, Amplitude: 40, PeriodMinutes: 240},
	}
	if age > 0 {
		at := time.Now().Add(-age).Truncate(time.Second)
		s.Activated = &at
	}
	return s, nil
}

func (s *State) validate() error {
	if b, err := hex.DecodeString(s.UID); err != nil || len(b) != 8 {
		return fmt.Errorf("uid %q: want 8 bytes of hex", s.UID)
	}
	if b, err := hex.DecodeString(s.PatchInfo); err != nil || len(b) != 6 {
		return fmt.Errorf("patchInfo %q: want 6 bytes of hex", s.PatchInfo)
	}
	if b, err := hex.DecodeString(strings.ReplaceAll(s.BLEAddress, ":", "")); err != nil || len(b) != 6 {
		return fmt.Errorf("bleAddress %q: want 6 bytes", s.BLEAddress)
	}
	if s.WearMinutes < 1 || s.WearMinutes > math.MaxUint16 {
		return fmt.Errorf("wearMinutes %d: want 1 to 65535", s.WearMinutes)
	}
	if i2 := s.Calibration.I2; i2 < 1 || i2 > len(t1) {
		return fmt.Errorf("calibration i2 %d: want 1 to %d", i2, len(t1))
	}
	return nil
}

func (s *State) uid() []byte       { return hexBytes(s.UID) }
func (s *State) patchInfo() []byte { return hexBytes(s.PatchInfo) }

// macLE is the BLE address least significant byte first, as the
// enable-streaming command returns it.
func (s *State) macLE() []byte {
	b := hexBytes(s.AdvertisedName())
	for i, j := 0, len(b)-1; i < j; i, j = i+1, j-1 {
		b[i], b[j] = b[j], b[i]
	}
	return b
}

// AdvertisedName is the BLE address in uppercase hex without separators:
// what LibreTransmitter makes of the enable-streaming reply, and the name it
// looks for when it scans.
func (s *State) AdvertisedName() string { return strings.ReplaceAll(s.BLEAddress, ":", "") }

// Serial is the serial number LibreTransmitter derives from the UID and
// patch info.
func (s *State) Serial() string {
	return SerialNumber(s.uid(), int(s.patchInfo()[2]>>4))
}

func (s *State) lifeCount(now time.Time) int {
	if s.Activated == nil {
		return 0
	}
	if now.Before(*s.Activated) {
		return 0
	}
	return int(now.Sub(*s.Activated) / time.Minute)
}

// fill makes up readings through life count lc.
func (s *State) fill(lc int) {
	for len(s.SkinRaw) < len(s.Readings) { // a state saved without temperatures
		s.SkinRaw = append(s.SkinRaw, uint16(s.Calibration.RawTemperature))
		s.TissueCelsius = append(s.TissueCelsius, s.Calibration.temperature())
	}
	for len(s.Readings) <= lc && len(s.Readings) <= s.WearMinutes {
		i := len(s.Readings)
		s.fillTemperature()
		if i < s.WarmupMinutes {
			s.Readings = append(s.Readings, 0)
			continue
		}
		v := s.model(i)
		s.Readings = append(s.Readings, uint16(math.Round(clampGlucose(v))))
	}
}

// fillTemperature makes up the next minute's skin and tissue temperature.
func (s *State) fillTemperature() {
	c := s.Calibration
	skin, lag := c.RawTemperature, 0.0
	if t := s.Temperature; t != nil {
		skin, lag = c.rawTemperatureFor(t.Celsius), t.TissueLagMinutes
	}
	celsius := c.temperatureFor(skin, c.RawTemperatureAdjustment)
	tissue := celsius
	if n := len(s.TissueCelsius); n > 0 && lag > 0 {
		prev := s.TissueCelsius[n-1]
		tissue = prev + (celsius-prev)*(1-math.Exp(-1/lag))
	}
	s.SkinRaw = append(s.SkinRaw, uint16(skin))
	s.TissueCelsius = append(s.TissueCelsius, tissue)
}

// record is what minute's measurement record holds: its raw glucose (made at
// the tissue temperature) and the thermistor's raw temperature and adjustment.
func (s *State) record(minute int) (raw uint16, rawTemperature, rawTemperatureAdjustment int) {
	c := s.Calibration
	rawTemperature, tissue := c.RawTemperature, c.temperature()
	if minute >= 0 && minute < len(s.SkinRaw) {
		rawTemperature, tissue = int(s.SkinRaw[minute]), s.TissueCelsius[minute]
	}
	return c.rawForGlucoseAt(s.reading(minute), tissue), rawTemperature, c.RawTemperatureAdjustment
}

func (s *State) model(i int) float64 {
	m := s.Glucose
	switch m.Kind {
	case "fixed":
		return m.Value
	case "walk":
		return s.walkMean() + mrand.NormFloat64()*walkNoiseSD
	default:
		period := m.PeriodMinutes
		if period <= 0 {
			period = 240
		}
		return m.Value + m.Amplitude*math.Sin(2*math.Pi*float64(i)/period)
	}
}

func (s *State) reading(lc int) uint16 {
	if lc < 0 || lc >= len(s.Readings) {
		return 0
	}
	return s.Readings[lc]
}

func LoadState(path string) (*State, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var s State
	if err := json.Unmarshal(b, &s); err != nil {
		return nil, err
	}
	if err := s.validate(); err != nil {
		return nil, err
	}
	return &s, nil
}

// Save writes the state atomically: to a temporary file, renamed over path.
func (s *State) Save(path string) error {
	b, err := json.Marshal(s)
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

func random(n int) []byte {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return b
}

// hexBytes decodes hex the sim itself wrote (validated on load).
func hexBytes(s string) []byte {
	b, err := hex.DecodeString(s)
	if err != nil {
		panic(err)
	}
	return b
}
