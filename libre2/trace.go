package libre2

import (
	"encoding/base64"
	"errors"
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"
)

// Trace is a real sensor's glucose, played back by the "trace" glucose
// model: the readings of one FRAM dump, minute by minute, oldest first.
type Trace struct {
	// Series holds mg/dL by minute, about 8 hours: the dump's 32 history
	// values (15 minutes apart) filled in minute by minute, then its 16
	// trend values (the last 15 minutes).
	Series []uint16
	// ReferenceMgdl is the glucose the reader showed when the dump was taken
	// ("// currentbg: 92" in the file); nil when the file does not say.
	ReferenceMgdl *int
	// Label is the file's first other comment, e.g. "Sensor 1, recorded
	// 2018-04-07": what the control panel calls it. "" if there is none.
	Label string
}

// ParseTrace reads a trace file: a Libre 1 or Libre 2 (EU) FRAM dump,
// unencrypted, base64-encoded. Lines starting with // are comments; one of
// them may give the reference glucose ("// currentbg: 92"); the first other
// one is the trace's label. The base64 may be split over several lines.
func ParseTrace(text string) (*Trace, error) {
	var ref *int
	label := ""
	var b64 strings.Builder
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "//") {
			c := strings.TrimSpace(strings.TrimPrefix(line, "//"))
			if k := strings.Index(c, ":"); k > 0 && strings.EqualFold(strings.TrimSpace(c[:k]), "currentbg") {
				if v, err := strconv.Atoi(strings.TrimSpace(c[k+1:])); err == nil {
					ref = &v
				}
			} else if label == "" {
				label = c
			}
			continue
		}
		b64.WriteString(strings.Join(strings.Fields(line), ""))
	}
	if b64.Len() == 0 {
		return nil, errors.New("no FRAM dump: want the 344-byte FRAM, base64-encoded")
	}
	fram, err := base64.StdEncoding.DecodeString(b64.String())
	if err != nil {
		if fram, err = base64.RawStdEncoding.DecodeString(b64.String()); err != nil {
			return nil, fmt.Errorf("the FRAM dump is not valid base64: %v", err)
		}
	}
	series, err := DecodeTraceFRAM(fram)
	if err != nil {
		return nil, err
	}
	return &Trace{Series: series, ReferenceMgdl: ref, Label: label}, nil
}

// DecodeTraceFRAM turns an unencrypted Libre 1 or Libre 2 (EU) FRAM dump into
// a minute-by-minute series of mg/dL, oldest first. Each record's mg/dL is what
// LibreTransmitter computes from it: glucoseValueFromRaw with the dump's own
// calibration (i1…i6) at the record's own temperature.
//
// The history records are placed as SensorData.dateOfMostRecentHistoryValue
// places them: the newest is (minutesSinceStart − 3) mod 15 + 3 minutes older
// than the newest trend record, or 15 minutes less when the history index is
// ahead of the minutes counter. The minutes between them are filled with a
// monotone cubic (PCHIP), so nothing overshoots the sensor's own values; the
// trend minutes are the trend's values. Records with raw glucose 0 (none)
// are skipped.
func DecodeTraceFRAM(fram []byte) ([]uint16, error) {
	if len(fram) != framSize {
		return nil, fmt.Errorf("a FRAM dump is %d bytes; this one is %d", framSize, len(fram))
	}
	body := fram[bodyStart:footerStart]
	if !framRegionCRCValid(body) {
		return nil, errors.New("the FRAM body's CRC does not check: not an unencrypted Libre 1 or Libre 2 (EU) FRAM dump")
	}
	nextTrend, nextHistory := int(body[2]), int(body[3])
	if nextTrend >= trendRecords || nextHistory >= historyRecords {
		return nil, fmt.Errorf("ring indices %d and %d: want under %d and %d", nextTrend, nextHistory, trendRecords, historyRecords)
	}
	cal := calibrationData(fram)
	if cal.I2 < 1 || cal.I2 > len(t1) || cal.I4 == cal.I3 {
		return nil, fmt.Errorf("the dump's calibration (i2 %d, i3 %d, i4 %d) is not usable", cal.I2, cal.I3, cal.I4)
	}
	mgdl := func(rec []byte) (float64, bool) {
		m := parseMeasurement(rec)
		if m.RawGlucose == 0 {
			return 0, false
		}
		v := cal.mgdlForRawAt(m.RawGlucose, cal.temperatureFor(m.RawTemperature, m.RawTemperatureAdjustment))
		return v, !math.IsNaN(v) && !math.IsInf(v, 0)
	}

	// Knots by minute, the newest trend record at minutesSinceStart.
	minutes := int(body[minutesOffset]) | int(body[minutesOffset+1])<<8
	knots := map[int]float64{}
	delay := (minutes-3)%15 + 3
	newestHistory := minutes - delay
	if (minutes-3)/15%historyRecords != nextHistory {
		newestHistory += 15
	}
	for k := 0; k < historyRecords; k++ {
		slot := mod(nextHistory-1-k, historyRecords)
		if v, ok := mgdl(body[historyOffset+slot*measurementSize:]); ok {
			knots[newestHistory-15*k] = v
		}
	}
	for k := 0; k < trendRecords; k++ {
		slot := mod(nextTrend-1-k, trendRecords)
		if v, ok := mgdl(body[trendOffset+slot*measurementSize:]); ok {
			knots[minutes-k] = v
		}
	}
	if len(knots) < 2 {
		return nil, fmt.Errorf("the dump has %d readings; a trace needs at least 2", len(knots))
	}
	xs := make([]int, 0, len(knots))
	for x := range knots {
		xs = append(xs, x)
	}
	sort.Ints(xs)
	ys := make([]float64, len(xs))
	for i, x := range xs {
		ys[i] = knots[x]
	}
	filled := pchipFill(xs, ys)
	series := make([]uint16, len(filled))
	for i, v := range filled {
		series[i] = uint16(math.Round(clampGlucose(v)))
	}
	return series, nil
}

// pchipFill is the monotone cubic (PCHIP, Fritsch–Carlson) through the knots
// (xs strictly increasing), at every whole x from xs[0] to xs[len-1]. Between
// two knots it stays within their values, and it passes through each knot.
func pchipFill(xs []int, ys []float64) []float64 {
	n := len(xs)
	h := make([]float64, n-1)
	delta := make([]float64, n-1)
	for k := 0; k < n-1; k++ {
		h[k] = float64(xs[k+1] - xs[k])
		delta[k] = (ys[k+1] - ys[k]) / h[k]
	}
	d := make([]float64, n)
	if n == 2 {
		d[0], d[1] = delta[0], delta[0]
	} else {
		for k := 1; k < n-1; k++ {
			if delta[k-1]*delta[k] <= 0 {
				continue
			}
			w1, w2 := 2*h[k]+h[k-1], h[k]+2*h[k-1]
			d[k] = (w1 + w2) / (w1/delta[k-1] + w2/delta[k])
		}
		d[0] = pchipEnd(h[0], h[1], delta[0], delta[1])
		d[n-1] = pchipEnd(h[n-2], h[n-3], delta[n-2], delta[n-3])
	}
	out := make([]float64, 0, xs[n-1]-xs[0]+1)
	for k := 0; k < n-1; k++ {
		for x := xs[k]; x < xs[k+1]; x++ {
			t := float64(x-xs[k]) / h[k]
			t2, t3 := t*t, t*t*t
			out = append(out, (2*t3-3*t2+1)*ys[k]+(t3-2*t2+t)*h[k]*d[k]+(-2*t3+3*t2)*ys[k+1]+(t3-t2)*h[k]*d[k+1])
		}
	}
	return append(out, ys[n-1])
}

// pchipEnd is the slope at an end knot: the three-point estimate, kept to the
// end interval's direction and to at most three times its slope, so the end
// interval stays monotone.
func pchipEnd(h0, h1, delta0, delta1 float64) float64 {
	d := ((2*h0+h1)*delta0 - h0*delta1) / (h0 + h1)
	switch {
	case math.Signbit(d) != math.Signbit(delta0) || delta0 == 0:
		return 0
	case math.Signbit(delta0) != math.Signbit(delta1) && math.Abs(d) > 3*math.Abs(delta0):
		return 3 * delta0
	}
	return d
}
