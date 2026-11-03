package libre2

import (
	"errors"
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
}

func NewSensor(state *State, path string) *Sensor {
	return &Sensor{state: state, path: path, lastSaved: -1}
}

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
}

func (s *Sensor) SetGlucoseMode(m GlucoseMode) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.state.Glucose = m
	s.changed()
}

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
		"calibration":      st.Calibration,
		"glucoseMode":      st.Glucose,
		"connected":        s.links > 0,
		"advertising":      s.advertising(now),
		"strictUnlock":     s.strictUnlock,
	}
	if now.Before(s.outOfRange) {
		out["outOfRangeUntil"] = s.outOfRange.Format(time.RFC3339)
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
