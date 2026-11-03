package libre2

import (
	"math"
	"time"
)

// walkNoiseSD is the standard deviation of the walk model's per-minute noise
// (see State.model).
const walkNoiseSD = 0.8

// NextReading is the reading the sensor will make up at the next minute.
type NextReading struct {
	Minute int `json:"minute"` // its life count
	// MgDl is 0 while the next minute is still in warmup: no reading.
	MgDl   int  `json:"mgdl"`
	Warmup bool `json:"warmup,omitempty"`
	// Estimate is true for the walk model, whose value is random and is made
	// only when its minute arrives: MgDl is then the mean of what it will be
	// (the previous reading drifting toward the mode's value), to within
	// about ±2·NoiseSD.
	Estimate bool    `json:"estimate"`
	NoiseSD  float64 `json:"noiseSD,omitempty"`
	At       string  `json:"at"`        // when it is made and sent, RFC3339
	AtMs     int64   `json:"atMs"`      // the same, in Unix milliseconds
	InSecs   float64 `json:"inSeconds"` // from now
}

// Trend is how the latest reading differs from the ones before it, in mg/dL;
// a delta is missing while that earlier minute has no reading.
type Trend struct {
	Delta1  *int `json:"delta1,omitempty"`
	Delta5  *int `json:"delta5,omitempty"`
	Delta15 *int `json:"delta15,omitempty"`
}

// nextReading is the next minute's reading at now, with mu held, or nil when
// there is none (not running). It does not touch the model's random
// sequence: fixed and sine are pure functions of the minute, and for walk it
// returns the model's mean, not a draw.
func (s *Sensor) nextReading(now time.Time) *NextReading {
	st := s.state
	if !st.running(now) {
		return nil
	}
	lc := st.lifeCount(now)
	next := lc + 1
	if next >= st.WearMinutes {
		return nil
	}
	st.fill(lc)
	at := st.Activated.Add(time.Duration(next) * time.Minute)
	n := &NextReading{Minute: next, At: at.Format(time.RFC3339), AtMs: at.UnixNano() / int64(time.Millisecond),
		InSecs: math.Max(0, at.Sub(now).Seconds())}
	if next < st.WarmupMinutes {
		n.Warmup = true
		return n
	}
	var v float64
	if st.Glucose.Kind == "walk" {
		n.Estimate, n.NoiseSD = true, walkNoiseSD
		v = st.walkMean()
	} else {
		v = st.model(next)
	}
	n.MgDl = int(math.Round(clampGlucose(v)))
	return n
}

// trend is the latest reading's trend at now, with mu held.
func (s *Sensor) trend(now time.Time) *Trend {
	st := s.state
	if st.Activated == nil {
		return nil
	}
	lc := st.framMinutes(now)
	cur := st.reading(lc)
	if cur == 0 {
		return nil
	}
	delta := func(m int) *int {
		if p := st.reading(lc - m); lc-m >= 0 && p != 0 {
			d := int(cur) - int(p)
			return &d
		}
		return nil
	}
	return &Trend{Delta1: delta(1), Delta5: delta(5), Delta15: delta(15)}
}

// History is the latest readings, for the control panel's chart.
type History struct {
	Minute int `json:"minute"` // the latest life count
	// FirstMinute is the life count of Values[0].
	FirstMinute int `json:"firstMinute"`
	// Values holds mg/dL by minute, oldest first; 0 is no reading (warmup).
	Values []uint16     `json:"mgdl"`
	Next   *NextReading `json:"next,omitempty"`
	Trend  *Trend       `json:"trend,omitempty"`
}

// History returns up to the last minutes readings.
func (s *Sensor) History(minutes int, now time.Time) History {
	s.mu.Lock()
	defer s.mu.Unlock()
	st := s.state
	h := History{Values: []uint16{}}
	if st.Activated == nil {
		return h
	}
	lc := st.framMinutes(now)
	st.fill(lc)
	first := lc - minutes + 1
	if minutes < 1 || first < 0 {
		first = 0
	}
	h.Minute, h.FirstMinute = lc, first
	for i := first; i <= lc; i++ {
		h.Values = append(h.Values, st.reading(i))
	}
	h.Next, h.Trend = s.nextReading(now), s.trend(now)
	return h
}

func clampGlucose(v float64) float64 { return math.Max(40, math.Min(400, v)) }

// walkMean is the mean of the walk model's next value.
func (s *State) walkMean() float64 {
	m := s.Glucose
	prev := m.Value
	if n := len(s.Readings); n > 0 && s.Readings[n-1] != 0 {
		prev = float64(s.Readings[n-1])
	}
	return prev + (m.Value-prev)*0.02
}
