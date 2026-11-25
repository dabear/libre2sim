package main

import (
	"encoding/json"
	"math"
	"os"
	"time"

	"libre2sim/libre2"
)

type Rec struct {
	Raw     int     `json:"raw"`
	AppMgDl float64 `json:"appmgdl"`
	Minute  int     `json:"minute"`
}
type Point struct {
	Minute int   `json:"minute"`
	True   int   `json:"true"`
	Window []Rec `json:"window"`
	Ahead  []int `json:"ahead"`
}

func clampU16(v float64) uint16 {
	if v < 40 {
		v = 40
	}
	if v > 400 {
		v = 400
	}
	return uint16(math.Round(v))
}

// ramp builds a piecewise-linear mg/dL series from (minute,value) knots, one value per minute.
func ramp(knots [][2]float64, total int) []uint16 {
	s := make([]uint16, total)
	for i := 0; i < total; i++ {
		t := float64(i)
		// find segment
		v := knots[len(knots)-1][1]
		for j := 0; j+1 < len(knots); j++ {
			a, b := knots[j], knots[j+1]
			if t >= a[0] && t <= b[0] {
				f := 0.0
				if b[0] != a[0] {
					f = (t - a[0]) / (b[0] - a[0])
				}
				v = a[1] + (b[1]-a[1])*f
				break
			}
		}
		s[i] = clampU16(v)
	}
	return s
}

func run(setup func(*libre2.State), samples int) []Point {
	st, err := libre2.NewState(14, 0, 0)
	if err != nil {
		panic(err)
	}
	t0 := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	st.Activated = &t0
	setup(st)
	s := libre2.NewSensor(st, "")
	var out []Point
	for m := 20; m <= samples; m++ {
		now := t0.Add(time.Duration(m) * time.Minute)
		h := s.History(16, 10, now)
		p := Point{Minute: h.Minute}
		if len(h.Values) > 0 {
			p.True = int(h.Values[len(h.Values)-1])
		}
		for i, r := range h.Records {
			p.Window = append(p.Window, Rec{Raw: r.Raw, AppMgDl: r.AppMgDl, Minute: h.FirstMinute + i})
		}
		for _, a := range h.Ahead {
			p.Ahead = append(p.Ahead, a.MgDl)
		}
		out = append(out, p)
	}
	return out
}

func main() {
	// A "meal" (fast rise, slow fall) and an "exercise" dip are the asymmetric cases
	// where an asymmetric projection matters most; sines at three rates cover symmetric dynamics.
	meal := ramp([][2]float64{{0, 95}, {30, 95}, {65, 215}, {90, 200}, {230, 110}, {300, 105}}, 320)
	exercise := ramp([][2]float64{{0, 150}, {20, 150}, {60, 62}, {75, 62}, {140, 145}, {300, 140}}, 320)

	scenarios := map[string]struct {
		setup   func(*libre2.State)
		samples int
	}{
		"sine_fast": {func(st *libre2.State) {
			st.Glucose = libre2.GlucoseMode{Kind: "sine", Value: 120, Amplitude: 60, PeriodMinutes: 60}
		}, 230},
		"sine_mid": {func(st *libre2.State) {
			st.Glucose = libre2.GlucoseMode{Kind: "sine", Value: 130, Amplitude: 70, PeriodMinutes: 120}
		}, 260},
		"sine_slow": {func(st *libre2.State) {
			st.Glucose = libre2.GlucoseMode{Kind: "sine", Value: 135, Amplitude: 55, PeriodMinutes: 240}
		}, 300},
		"meal": {func(st *libre2.State) {
			st.Glucose = libre2.GlucoseMode{Kind: "trace", Trace: "meal", TraceStartMinute: 0, TraceMinutes: len(meal)}
			st.TraceSeries = meal
		}, 300},
		"exercise": {func(st *libre2.State) {
			st.Glucose = libre2.GlucoseMode{Kind: "trace", Trace: "exercise", TraceStartMinute: 0, TraceMinutes: len(exercise)}
			st.TraceSeries = exercise
		}, 300},
	}
	out := map[string][]Point{}
	for name, sc := range scenarios {
		out[name] = run(sc.setup, sc.samples)
	}
	_ = json.NewEncoder(os.Stdout).Encode(out)
}
