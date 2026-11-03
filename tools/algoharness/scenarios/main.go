// Command scenarios writes libre2sim's GET /readings for a set of temperature and
// glucose scenarios, made on a simulated clock, for algoharness to read.
//
//	GO111MODULE=on go run ./tools/algoharness/scenarios <output directory>
package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"libre2sim/libre2"
)

// A scenario: a sensor worn 3 hours at the glucose model, then its steps,
// each at a minute from the start, and the readings of the last 60 minutes.
type scenario struct {
	name    string
	about   string
	glucose libre2.GlucoseMode
	steps   []step
}

type step struct {
	minute      int
	temperature *libre2.TemperatureMode
	glucose     *libre2.GlucoseMode
}

func temp(c, lag float64) *libre2.TemperatureMode {
	return &libre2.TemperatureMode{Celsius: c, TissueLagMinutes: lag}
}

var fixed120 = libre2.GlucoseMode{Kind: "fixed", Value: 120}

var scenarios = []scenario{
	{"steady", "31.6 °C throughout", fixed120, nil},
	{"cold-steady", "24 °C skin and tissue throughout", fixed120, []step{{0, temp(24, 0), nil}}},
	{"step-no-lag", "skin 31.6 → 27 °C at minute 20; the tissue follows at once", fixed120, []step{{20, temp(27, 0), nil}}},
	{"step-lag5", "skin 31.6 → 27 °C at minute 20; the tissue follows over 5 minutes", fixed120, []step{{20, temp(27, 5), nil}}},
	{"step-lag10", "skin 31.6 → 27 °C at minute 20; the tissue follows over 10 minutes", fixed120, []step{{20, temp(27, 10), nil}}},
	{"blip-no-lag", "skin 31.6 → 29 °C for 3 minutes at minute 20 (a cold draught); the tissue follows at once", fixed120,
		[]step{{20, temp(29, 0), nil}, {23, temp(31.6, 0), nil}}},
	{"blip-lag5", "skin 31.6 → 29 °C for 3 minutes at minute 20; the tissue follows over 5 minutes", fixed120,
		[]step{{20, temp(29, 5), nil}, {23, temp(31.6, 5), nil}}},
	{"falling-sine", "glucose falling through a sine (120 ± 60, 120 min) at 31.6 °C", libre2.GlucoseMode{Kind: "sine", Value: 120, Amplitude: 60, PeriodMinutes: 120}, nil},
	{"low-35", "glucose 35 mg/dL, below the sensor's 40–500 range", libre2.GlucoseMode{Kind: "fixed", Value: 35}, nil},
}

func main() {
	if len(os.Args) != 2 {
		fmt.Fprintln(os.Stderr, "usage: scenarios <output directory>")
		os.Exit(2)
	}
	dir := os.Args[1]
	os.MkdirAll(dir, 0o755)
	for _, sc := range scenarios {
		st, err := libre2.NewState(15, time.Hour, 3*time.Hour)
		if err != nil {
			panic(err)
		}
		st.Glucose = sc.glucose
		s := libre2.NewSensor(st, filepath.Join(dir, sc.name+".state.json"))
		start := *st.Activated
		start = start.Add(3 * time.Hour)
		for _, p := range sc.steps {
			at := start.Add(time.Duration(p.minute) * time.Minute)
			s.Tick(at)
			if p.temperature != nil {
				if err := s.SetTemperature(*p.temperature, at); err != nil {
					panic(err)
				}
			}
			if p.glucose != nil {
				s.SetGlucoseMode(*p.glucose)
			}
		}
		end := start.Add(60 * time.Minute)
		s.Tick(end)
		b, _ := json.Marshal(s.History(60, 0, end))
		os.WriteFile(filepath.Join(dir, sc.name+".json"), b, 0o644)
		os.Remove(filepath.Join(dir, sc.name+".state.json"))
		fmt.Printf("%-13s %s\n", sc.name, sc.about)
	}
}
