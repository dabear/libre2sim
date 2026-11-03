package libre2

import (
	"fmt"
	"math"
	"time"
)

// walkNoiseSD is the standard deviation of the walk model's per-minute noise
// (see State.model).
const walkNoiseSD = 0.8

// walkReversion is how far the walk model moves toward its value each minute.
const walkReversion = 0.02

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
// there is none (not running).
func (s *Sensor) nextReading(now time.Time) *NextReading {
	up, _ := s.upcoming(now, 1)
	if len(up) == 0 {
		return nil
	}
	return &up[0]
}

// upcoming is the readings of the next n minutes at now, with mu held, and the
// minute the sensor's wear ends if that is within them (else 0). It does not
// touch the model's random sequence: fixed and sine are pure functions of the
// minute, and for walk it returns the model's mean, not a draw, with the
// spread of the draws that far ahead.
func (s *Sensor) upcoming(now time.Time, n int) ([]NextReading, int) {
	st := s.state
	if !st.running(now) {
		return nil, 0
	}
	lc := st.lifeCount(now)
	st.fill(lc)
	walk := st.Glucose.Kind == "walk"
	prev := st.walkPrev()
	var variance float64
	var out []NextReading
	for k := 1; k <= n; k++ {
		m := lc + k
		if m >= st.WearMinutes {
			return out, st.WearMinutes
		}
		at := st.Activated.Add(time.Duration(m) * time.Minute)
		r := NextReading{Minute: m, At: at.Format(time.RFC3339), AtMs: at.UnixNano() / int64(time.Millisecond),
			InSecs: math.Max(0, at.Sub(now).Seconds())}
		var v float64
		switch {
		case m < st.WarmupMinutes:
			r.Warmup = true
		case walk:
			// Each minute moves 2% of the way to Value, plus noise; the mean
			// follows the same path and the noise adds up.
			prev += (st.Glucose.Value - prev) * walkReversion
			variance = variance*(1-walkReversion)*(1-walkReversion) + walkNoiseSD*walkNoiseSD
			r.Estimate, r.NoiseSD = true, math.Round(math.Sqrt(variance)*100)/100
			v = prev
		default:
			v = st.model(m)
		}
		if !r.Warmup {
			r.MgDl = int(math.Round(clampGlucose(v)))
		}
		out = append(out, r)
	}
	return out, 0
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
	// Ahead is the readings of the next minutes, when asked for; WearEnds is
	// the minute the wear ends if that comes first (no readings from then).
	Ahead    []NextReading `json:"ahead,omitempty"`
	WearEnds int           `json:"wearEnds,omitempty"`
	// Records is how each of Values goes over BLE, index for index; Ahead's
	// are in AheadRecords. Calibration and Temperature are what the app needs
	// besides a record's raw glucose to compute glucose from it.
	Records      []RecordDetail         `json:"records"`
	AheadRecords []RecordDetail         `json:"aheadRecords,omitempty"`
	Calibration  map[string]int         `json:"calibration,omitempty"`
	Temperature  map[string]interface{} `json:"temperature,omitempty"`
	ActivatedMs  int64                  `json:"activatedMs,omitempty"`
}

// RecordDetail is a reading as a BLE packet record carries it.
type RecordDetail struct {
	// Raw is the 14-bit raw glucose, 0 for a minute without a reading.
	Raw int `json:"raw"`
	// RawTemperature and RawTemperatureAdjustment are the record's
	// temperature fields; Celsius is what the app's thermistor model makes of
	// them, TissueCelsius the temperature Raw was made at.
	RawTemperature           int     `json:"rawTemperature"`
	RawTemperatureAdjustment int     `json:"rawTemperatureAdjustment"`
	Celsius                  float64 `json:"celsius"`
	TissueCelsius            float64 `json:"tissueCelsius"`
	// AppMgDl is the unrounded mg/dL LibreTransmitter computes from Raw at
	// the record's own temperature (glucoseValueFromRaw); 0 when Raw is 0.
	// It differs from the reading while the tissue lags the skin.
	AppMgDl float64 `json:"appMgdl"`
	// BLE is the 4-byte record, in hex.
	BLE string `json:"ble"`
}

// recordDetail is how mgdl goes over BLE with raw temperature rawT, its raw
// glucose made at tissue °C.
func recordDetail(c Calibration, mgdl uint16, rawT int, tissue float64) RecordDetail {
	raw := c.rawForGlucoseAt(mgdl, tissue)
	adj := c.RawTemperatureAdjustment
	b := make([]byte, bleRecordSize)
	putBLERecord(b, raw, rawT, adj)
	celsius := c.temperatureFor(rawT, adj)
	d := RecordDetail{Raw: int(raw), RawTemperature: rawT, RawTemperatureAdjustment: adj, BLE: fmt.Sprintf("% x", b),
		Celsius: math.Round(celsius*100) / 100, TissueCelsius: math.Round(tissue*100) / 100}
	if raw != 0 {
		d.AppMgDl = math.Round(c.mgdlForRawAt(int(raw), celsius)*100) / 100
	}
	return d
}

// History returns up to the last minutes readings, and the next ahead ones.
func (s *Sensor) History(minutes, ahead int, now time.Time) History {
	s.mu.Lock()
	defer s.mu.Unlock()
	st := s.state
	h := History{Values: []uint16{}, Records: []RecordDetail{}}
	if st.Activated == nil {
		return h
	}
	h.Calibration, h.Temperature = st.Calibration.info(), st.measurementTemperature()
	h.ActivatedMs = st.Activated.UnixNano() / int64(time.Millisecond)
	lc := st.framMinutes(now)
	st.fill(lc)
	first := lc - minutes + 1
	if minutes < 1 || first < 0 {
		first = 0
	}
	h.Minute, h.FirstMinute = lc, first
	for i := first; i <= lc; i++ {
		v := st.reading(i)
		h.Values = append(h.Values, v)
		_, rawT, _ := st.record(i)
		tissue := st.Calibration.temperature()
		if i < len(st.TissueCelsius) {
			tissue = st.TissueCelsius[i]
		}
		h.Records = append(h.Records, recordDetail(st.Calibration, v, rawT, tissue))
	}
	h.Next, h.Trend = s.nextReading(now), s.trend(now)
	if ahead > 0 {
		h.Ahead, h.WearEnds = s.upcoming(now, ahead)
		// The coming minutes at the current temperature setting, the tissue
		// still approaching it.
		c := st.Calibration
		rawT, lag := c.RawTemperature, 0.0
		if t := st.Temperature; t != nil {
			rawT, lag = c.rawTemperatureFor(t.Celsius), t.TissueLagMinutes
		}
		skin := c.temperatureFor(rawT, c.RawTemperatureAdjustment)
		tissue := skin
		if n := len(st.TissueCelsius); n > 0 {
			tissue = st.TissueCelsius[n-1]
		}
		for _, r := range h.Ahead {
			if lag > 0 {
				tissue += (skin - tissue) * (1 - math.Exp(-1/lag))
			} else {
				tissue = skin
			}
			h.AheadRecords = append(h.AheadRecords, recordDetail(c, uint16(r.MgDl), rawT, tissue))
		}
	}
	return h
}

func clampGlucose(v float64) float64 { return math.Max(40, math.Min(400, v)) }

// walkMean is the mean of the walk model's next value.
func (s *State) walkMean() float64 {
	prev := s.walkPrev()
	return prev + (s.Glucose.Value-prev)*walkReversion
}

// walkPrev is the value the walk model's next step starts from.
func (s *State) walkPrev() float64 {
	if n := len(s.Readings); n > 0 && s.Readings[n-1] != 0 {
		return float64(s.Readings[n-1])
	}
	return s.Glucose.Value
}
