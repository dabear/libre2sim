package libre2

import (
	"errors"
	"fmt"
	"log"
	"sync"
	"time"
)

// Sensor is the emulated Libre 2: its persisted state plus what is only true
// while the emulator runs.
type Sensor struct {
	path string

	mu        sync.Mutex
	state     *State
	lastSaved int
	retired   bool

	// fram is the encrypted FRAM image the current NFC scan is reading,
	// taken at framAt (see readBlocks).
	fram   []byte
	framAt time.Time

	// The BLE side (see ble.go). None of it is saved.
	links        int       // links up: 1 while a central holds the link
	outOfRange   time.Time // unreachable until then
	strictUnlock bool      // refuse unlock counts at or below UnlockCount
	nonce        uint16    // the last packet's n0 n1
	fault        string    // armed for the next packet only (see notifies); "" for none

	events                          *EventLog
	unlocksAccepted, unlocksRefused int  // BLE unlocks since launch
	expiryLogged                    bool // the worn-out event was recorded
}

func NewSensor(state *State, path string) *Sensor {
	return &Sensor{state: state, path: path, lastSaved: -1, events: NewEventLog(DefaultEventCap)}
}

// Events is the sensor's event log: what the control panel's log shows.
func (s *Sensor) Events() *EventLog { return s.events }

func (s *Sensor) Serial() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.state.Serial()
}

// AdvertisedName is the BLE address as uppercase hex without separators.
func (s *Sensor) AdvertisedName() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.state.AdvertisedName()
}

func (s *Sensor) save() {
	if s.retired {
		return
	}
	if err := s.state.Save(s.path); err != nil {
		log.Printf("saving %s: %v", s.path, err)
	}
}

// changed is called, with mu held, whenever the FRAM's contents change other
// than by the clock: the next read starts a fresh image.
func (s *Sensor) changed() {
	s.fram = nil
	s.save()
}

// Retire stops the sensor writing its state file, before another replaces it.
func (s *Sensor) Retire() {
	s.mu.Lock()
	s.retired = true
	s.mu.Unlock()
}

// Tick advances the sensor's clock: readings are made up minute by minute
// and saved. It returns the life count.
func (s *Sensor) Tick(now time.Time) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	lc := s.state.lifeCount(now)
	if s.state.Activated != nil {
		s.state.fill(lc)
	}
	if lc != s.lastSaved {
		s.lastSaved = lc
		s.save()
	}
	if s.state.Activated != nil && s.state.Terminated == nil && lc >= s.state.WearMinutes && !s.expiryLogged {
		s.expiryLogged = true
		s.events.Add(CatSensor, "expired", "the sensor is worn out (%d minutes)", s.state.WearMinutes)
	}
	return lc
}

// Activate starts a sensor that is still in its applicator, as a reader app
// would. LibreTransmitter cannot do this, so the control API does.
func (s *Sensor) Activate(now time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.state.Activated != nil {
		return errors.New("already activated")
	}
	at := now.Truncate(time.Second)
	s.state.Activated = &at
	s.lastSaved = -1
	s.changed()
	s.events.Add(CatSensor, "activated", "activated at %s", at.Format(time.RFC3339))
	return nil
}

// EndWarmup cuts the warmup short: the next minute is the first with a
// reading, and minutes already made up stay without one, as a glucose mode
// change applies only from the next minute. A real sensor cannot do this.
func (s *Sensor) EndWarmup(now time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.state.running(now) {
		return errors.New("the sensor is not running")
	}
	lc := s.state.lifeCount(now)
	if lc >= s.state.WarmupMinutes {
		return errors.New("the sensor is not in warmup")
	}
	if lc+1 == s.state.WarmupMinutes {
		return errors.New("the warmup already ends at the next minute")
	}
	s.state.WarmupMinutes = lc + 1
	s.changed()
	s.events.Add(CatSensor, "warmup-ended", "warmup ended early: the first reading is minute %d", lc+1)
	return nil
}

// Terminate ends the sensor now: its FRAM says shut down and it no longer
// enables streaming.
func (s *Sensor) Terminate(now time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.state.Terminated == nil {
		s.state.Terminated = &Termination{LifeCount: uint16(s.state.framMinutes(now))}
	}
	s.changed()
	s.events.Add(CatSensor, "terminated", "terminated at minute %d", s.state.Terminated.LifeCount)
}

func (s *Sensor) SetGlucoseMode(m GlucoseMode) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.state.Glucose = m
	s.changed()
	s.events.Add(CatSensor, "glucose-mode", "glucose mode now %s (amplitude %g, period %g min)", m, m.Amplitude, m.PeriodMinutes)
}

// SetTemperature sets the skin temperature, and the tissue's lag behind it,
// from the next minute on.
func (s *Sensor) SetTemperature(m TemperatureMode, now time.Time) error {
	if m.Celsius < minCelsius || m.Celsius > maxCelsius || m.TissueLagMinutes < 0 || m.TissueLagMinutes > 60 {
		return fmt.Errorf("want celsius %g…%g and tissueLagMinutes 0…60", minCelsius, maxCelsius)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.state.Activated != nil {
		s.state.fill(s.state.lifeCount(now))
	}
	s.state.Temperature = &m
	s.changed()
	s.events.Add(CatSensor, "temperature", "skin temperature now %.1f °C (raw %d), the tissue lagging %g min",
		m.Celsius, s.state.Calibration.rawTemperatureFor(m.Celsius), m.TissueLagMinutes)
	return nil
}

// The skin temperatures POST /temperature takes.
const (
	minCelsius = 20.0
	maxCelsius = 42.0
)

func (s *Sensor) Status() map[string]interface{} {
	now := time.Now()
	s.mu.Lock()
	defer s.mu.Unlock()
	st := s.state
	out := map[string]interface{}{
		"uid":              st.UID,
		"serial":           st.Serial(),
		"patchInfo":        st.PatchInfo,
		"bleAddress":       st.BLEAddress,
		"advertisedName":   st.AdvertisedName(),
		"framState":        st.framState(now),
		"wearMinutes":      st.WearMinutes,
		"warmupMinutes":    st.WarmupMinutes,
		"streamingEnabled": st.StreamingCode != nil,
		"unlockCount":      st.UnlockCount,
		"calibration":      st.Calibration.info(),
		"temperature":      st.measurementTemperature(),
		"glucoseMode":      st.Glucose,
		"connected":        s.links > 0,
		"advertising":      s.advertising(now),
		"strictUnlock":     s.strictUnlock,
		"unlocksAccepted":  s.unlocksAccepted,
		"unlocksRefused":   s.unlocksRefused,
	}
	if n := s.nextReading(now); n != nil {
		out["nextReading"] = n
	}
	if t := s.trend(now); t != nil {
		out["trend"] = t
	}
	if now.Before(s.outOfRange) {
		out["outOfRangeUntil"] = s.outOfRange.Format(time.RFC3339)
	}
	if s.fault != "" {
		out["fault"] = s.fault
	}
	if st.StreamingCode != nil {
		out["streamingCode"] = *st.StreamingCode
	}
	if st.Activated != nil {
		lc := st.framMinutes(now)
		st.fill(lc)
		out["activated"] = st.Activated.Format(time.RFC3339)
		out["lifeCount"] = lc
		if g := st.reading(lc); g != 0 {
			out["glucose"] = g
		}
	}
	if st.Terminated != nil {
		out["terminated"] = st.Terminated
	}
	return out
}

// Reading is the made-up glucose in mg/dL for life count lc; 0 for none.
func (s *Sensor) Reading(lc int) uint16 {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.state.reading(lc)
}

// Calibration is the sensor's calibration with the temperature its records
// carry: what it takes to turn a raw value into mg/dL.
func (s *Sensor) Calibration() Calibration {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.state.Calibration
}

// StrictUnlock is whether the sensor refuses replayed unlock counts.
func (s *Sensor) StrictUnlock() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.strictUnlock
}

// unlockResult counts a BLE unlock, accepted or refused.
func (s *Sensor) unlockResult(ok bool) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	if ok {
		s.unlocksAccepted++
		return s.unlocksAccepted
	}
	s.unlocksRefused++
	return s.unlocksRefused
}
