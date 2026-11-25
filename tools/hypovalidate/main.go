// SPDX-License-Identifier: GPL-3.0-or-later
//
// Command hypovalidate writes libre2sim GET /readings JSON for a set of
// LOW-GLUCOSE scenarios, on a simulated clock (no waiting). It is the
// ground-truth source for the independent-impl hypo validation: each scenario
// drives glucose through / into / out of the hypo zone with real, CRC-valid
// FRAM records, and records the COMMANDED true glucose per minute (History
// Values) alongside each record's raw glucose/temperature fields and 4-byte
// BLE record.
//
// The companion Python (independent-impl/hypo_validate.py) reads these files,
// runs the independent glucose implementation per record, and reports hypo-zone
// agreement with truth. The same files' `ble`/records feed the Abbott OOP
// oracle (see HYPO_VALIDATION.md) for the three-way compare.
//
//	GO111MODULE=on go run ./tools/hypovalidate /tmp/hypo
//
// Trajectories are scripted minute-by-minute: before each tick the glucose mode
// is set to that minute's target (fixed), so any shape is possible. Truth is
// read back from History, so it is always what the sim actually committed.
package main

import (
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"libre2sim/libre2"
)

func steadyAt(v float64) func(int) float64 { return func(int) float64 { return v } }

// tempStep sets the skin temperature (and tissue lag) from a given minute on.
type tempStep struct {
	minute          int
	celsius, lagMin float64
}

type scenario struct {
	name  string
	about string
	// mgdl returns the commanded glucose at life-minute m (0-based from the
	// scenario start, after warmup). It is clamped to the sim's model only by
	// what the sensor does with it; we read truth back from History.
	mgdl  func(m int) float64
	temps []tempStep
	// tempFn, when set, gives the skin temperature (°C) at each minute, applied
	// every minute (trace replay uses it to reproduce each reading's temperature).
	tempFn func(m int) float64
	// adjFn, when set with tempFn, gives each minute's raw temperature
	// adjustment (trace replay: the dump's records' own, not the sim default).
	adjFn func(m int) int
	mins  int // window length in minutes
}

// ramp builds a piecewise-linear trajectory from (minute,value) knots.
func ramp(knots [][2]float64) func(int) float64 {
	return func(m int) float64 {
		x := float64(m)
		if x <= knots[0][0] {
			return knots[0][1]
		}
		for i := 1; i < len(knots); i++ {
			if x <= knots[i][0] {
				x0, y0 := knots[i-1][0], knots[i-1][1]
				x1, y1 := knots[i][0], knots[i][1]
				if x1 == x0 {
					return y1
				}
				return y0 + (y1-y0)*(x-x0)/(x1-x0)
			}
		}
		return knots[len(knots)-1][1]
	}
}

const normal = 31.6 // steady skin °C used elsewhere in the harness

// framEvery is how often (minutes) a decrypted-FRAM "scan" is emitted; override
// with HYPO_FRAMEVERY to cut the number of scans on long trajectories.
var framEvery = 5

func init() {
	if v := os.Getenv("HYPO_FRAMEVERY"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			framEvery = n
		}
	}
}

var scenarios = []scenario{
	{"steady-40", "fixed 40 mg/dL (hypo floor of the reportable range)", func(int) float64 { return 40 }, nil, nil, nil, 90},
	{"steady-54", "fixed 54 mg/dL (clinical level-2 hypo)", func(int) float64 { return 54 }, nil, nil, nil, 90},
	{"steady-70", "fixed 70 mg/dL (hypo threshold)", func(int) float64 { return 70 }, nil, nil, nil, 90},
	{"fall-slow", "110 → 45 at −1 mg/dL/min, then hold 45",
		ramp([][2]float64{{0, 110}, {65, 45}, {120, 45}}), nil, nil, nil, 120},
	{"fall-fast", "110 → 45 at −3 mg/dL/min, hold, then +2/min recovery",
		ramp([][2]float64{{0, 110}, {22, 45}, {45, 45}, {78, 110}, {120, 110}}), nil, nil, nil, 120},
	{"vshape", "110 → 45 (−3) → hold 10 → 110 (+2): projection on fall then rise",
		ramp([][2]float64{{0, 110}, {22, 45}, {32, 45}, {65, 110}, {120, 110}}), nil, nil, nil, 120},
	{"double-dip", "110→55→80→50→100: repeated lows (state & projection)",
		ramp([][2]float64{{0, 110}, {25, 55}, {45, 80}, {70, 50}, {110, 100}, {120, 100}}), nil, nil, nil, 120},
	{"fall-cold", "fall to 45, THEN skin 31.6→26 °C (lag 5) at min 70 while already low: thermistor error at a true low",
		ramp([][2]float64{{0, 110}, {65, 45}, {120, 45}}),
		[]tempStep{{70, 26, 5}}, nil, nil, 120},
	{"steady-54-cold", "fixed 54 mg/dL, skin 31.6→27 °C (lag 5) at min 30: isolates temp error at a fixed low",
		func(int) float64 { return 54 },
		[]tempStep{{30, 27, 5}}, nil, nil, 90},
}

func main() {
	if len(os.Args) != 2 {
		fmt.Fprintln(os.Stderr, "usage: hypovalidate <output directory>  (env HYPO_SWEEP=\"45,54,...\" emits only steady-<v>)")
		os.Exit(2)
	}
	dir := os.Args[1]
	if err := os.MkdirAll(dir, 0o755); err != nil {
		panic(err)
	}
	// An optional calibration override (HYPO_TRACE sets it to the real sensor's).
	var traceCal libre2.Calibration
	useTraceCal := false
	// HYPO_SWEEP="45,54,70,..." replaces the scenario set with steady levels, for
	// characterizing Abbott's calibration decode of the simulator FRAM.
	// HYPO_TRACE=<file> replays a REAL sensor's trajectory: the file holds the
	// decrypted 344-byte FRAM as hex. DecodeTraceFRAM reconstructs the ~8 h
	// minute-by-minute glucose series (the dump's own calibration, PCHIP-filled
	// between the 15-min history knots), and we re-emit it as a sequence of FRAM
	// scans so the oracle can be driven over the whole history (warm vs cold).
	if tf := os.Getenv("HYPO_TRACE"); tf != "" {
		raw, err := os.ReadFile(tf)
		if err != nil {
			panic(err)
		}
		fram, err := hex.DecodeString(strings.Join(strings.Fields(string(raw)), ""))
		if err != nil {
			panic(err)
		}
		series, temps, adjs, err := libre2.DecodeTraceFRAMFull(fram)
		if err != nil {
			panic(err)
		}
		// Replay with the REAL sensor's calibration (not framTemplate's) AND each
		// reading's own inferred temperature, so the re-emitted FRAMs decode the
		// same way under Abbott as the original dump — removing the simulator
		// calibration and temperature offsets for the oracle cross-check.
		traceCal = libre2.CalibrationFromFRAM(fram)
		useTraceCal = true
		clampIdx := func(m, n int) int {
			if m < 0 {
				return 0
			}
			if m >= n {
				return n - 1
			}
			return m
		}
		scenarios = []scenario{{
			name:   "trace-replay",
			about:  fmt.Sprintf("real sensor trajectory, %d min, %d→%d mg/dL", len(series), series[0], series[len(series)-1]),
			mgdl:   func(m int) float64 { return float64(series[clampIdx(m, len(series))]) },
			tempFn: func(m int) float64 { return temps[clampIdx(m, len(temps))] },
			adjFn:  func(m int) int { return adjs[clampIdx(m, len(adjs))] },
			mins:   len(series) - 1,
		}}
	}
	// HYPO_RAMPS=1 emits constant-rate ramps through 90 mg/dL at ±1/2/3 mg/dL/min
	// (truth==90 at the scan at minute 15) for the projection-horizon fit, plus a
	// steady-90 baseline. projection(rate) = Abbott(ramp) − Abbott(steady-90).
	if os.Getenv("HYPO_RAMPS") != "" {
		scenarios = []scenario{{"steady-90", "fixed 90 mg/dL (projection baseline)", steadyAt(90), nil, nil, nil, 30}}
		for _, r := range []float64{1, 2, 3, -1, -2, -3} {
			rr := r
			dir := "up"
			if rr < 0 {
				dir = "dn"
			}
			scenarios = append(scenarios, scenario{
				name:  fmt.Sprintf("ramp-%s%d", dir, int(math.Abs(rr))),
				about: fmt.Sprintf("through 90 at %+g mg/dL/min (truth=90 at min 15)", rr),
				mgdl:  func(m int) float64 { return 90 + rr*float64(m-15) }, mins: 30})
		}
	}
	// HYPO_TEMPS="54,90" emits, for each glucose, a steady scenario at each of a
	// fixed set of skin temperatures, to probe Abbott's thermistor model. At fixed
	// glucose the calibration offset is constant, so Abbott(T)−Abbott(31.6) is the
	// pure temperature response; our impl is flat (raw is made at the same temp).
	if tg := os.Getenv("HYPO_TEMPS"); tg != "" {
		scenarios = nil
		temps := []float64{25, 28, 31.6, 34, 37}
		for _, tok := range strings.Split(tg, ",") {
			g, err := strconv.ParseFloat(strings.TrimSpace(tok), 64)
			if err != nil {
				panic(err)
			}
			for _, T := range temps {
				scenarios = append(scenarios, scenario{
					name:  fmt.Sprintf("temp-g%03d-t%03d", int(g), int(T*10)),
					about: fmt.Sprintf("fixed %g mg/dL at %g °C steady", g, T),
					mgdl:  steadyAt(g), temps: []tempStep{{0, T, 0}}, mins: 40})
			}
		}
	}
	// HYPO_STEP=1 emits a single glucose step 90→130 at minute 15 (flat either
	// side), to characterize Abbott's stateful smoother: after the step the slope
	// is 0 so projection drops out, and the scan-by-scan settling from 106→146 is
	// the smoother's step response.
	if os.Getenv("HYPO_STEP") != "" {
		scenarios = []scenario{{"step-90-130", "glucose step 90→130 at min 15",
			func(m int) float64 {
				if m < 15 {
					return 90
				}
				return 130
			}, nil, nil, nil, 45}}
	}
	if sw := os.Getenv("HYPO_SWEEP"); sw != "" {
		scenarios = nil
		for _, tok := range strings.Split(sw, ",") {
			v, err := strconv.ParseFloat(strings.TrimSpace(tok), 64)
			if err != nil {
				panic(err)
			}
			scenarios = append(scenarios, scenario{
				name: fmt.Sprintf("sweep-%03d", int(v)), about: fmt.Sprintf("fixed %g mg/dL", v),
				mgdl: steadyAt(v), mins: 40})
		}
	}
	index := map[string]string{}
	for _, sc := range scenarios {
		st, err := libre2.NewState(15, time.Hour, 3*time.Hour)
		if err != nil {
			panic(err)
		}
		st.SetGen2(true) // Libre 2: the oracle consumes a Gen2 decrypted FRAM
		if useTraceCal {
			// Override only the calibration coefficients i1..i6 with the real
			// sensor's; keep the default thermistor reference fields
			// (RawTemperature/Adjustment) so the temperature model stays valid.
			// The records' own temperature and adjustment come per minute via
			// tempFn/adjFn below.
			st.Calibration.I1 = traceCal.I1
			st.Calibration.I2 = traceCal.I2
			st.Calibration.I3 = traceCal.I3
			st.Calibration.I4 = traceCal.I4
			st.Calibration.I5 = traceCal.I5
			st.Calibration.I6 = traceCal.I6
		}
		st.Glucose = libre2.GlucoseMode{Kind: "fixed", Value: sc.mgdl(0)}
		s := libre2.NewSensor(st, filepath.Join(dir, sc.name+".state.json"))
		start := st.Activated.Add(3 * time.Hour) // past warmup
		if _, err := s.Gen2TestPairing(start); err != nil {
			panic(err) // enables Gen2 streaming / FRAM access
		}
		ti := 0
		var frams []map[string]any
		for m := 0; m <= sc.mins; m++ {
			at := start.Add(time.Duration(m) * time.Minute)
			// Apply any temperature step due at this minute.
			for ti < len(sc.temps) && sc.temps[ti].minute == m {
				if err := s.SetTemperature(libre2.TemperatureMode{
					Celsius: sc.temps[ti].celsius, TissueLagMinutes: sc.temps[ti].lagMin}, at); err != nil {
					panic(err)
				}
				ti++
			}
			// Per-minute skin temperature (trace replay), no tissue lag, with
			// the record's own raw temperature adjustment when known.
			if sc.tempFn != nil {
				tm := libre2.TemperatureMode{Celsius: sc.tempFn(m)}
				if sc.adjFn != nil {
					a := sc.adjFn(m)
					tm.Adjustment = &a
				}
				if err := s.SetTemperature(tm, at); err != nil {
					panic(err)
				}
			}
			// Command this minute's glucose (effective at the tick).
			s.SetGlucoseMode(libre2.GlucoseMode{Kind: "fixed", Value: round(sc.mgdl(m))})
			s.Tick(at)
			// Emit a decrypted FRAM "scan" every framEvery minutes: the oracle
			// is stateful and must be driven over the trajectory, not one frame.
			if m%framEvery == 0 {
				tp, err := s.Gen2TestPairing(at)
				if err != nil {
					panic(err)
				}
				frams = append(frams, map[string]any{"minute": m, "truth": round(sc.mgdl(m)), "fram": tp.FRAM})
			}
		}
		fb, _ := json.MarshalIndent(map[string]any{"scenario": sc.name, "uid": "", "frams": frams}, "", " ")
		os.WriteFile(filepath.Join(dir, sc.name+".frams.json"), fb, 0o644)
		end := start.Add(time.Duration(sc.mins) * time.Minute)
		h := s.History(sc.mins, 0, end)
		b, _ := json.MarshalIndent(h, "", " ")
		out := filepath.Join(dir, sc.name+".json")
		if err := os.WriteFile(out, b, 0o644); err != nil {
			panic(err)
		}
		os.Remove(filepath.Join(dir, sc.name+".state.json"))
		index[sc.name] = sc.about
		fmt.Printf("%-12s %s\n", sc.name, sc.about)
	}
	ib, _ := json.MarshalIndent(index, "", " ")
	os.WriteFile(filepath.Join(dir, "index.json"), ib, 0o644)
}

func round(v float64) float64 { return math.Round(v) }
