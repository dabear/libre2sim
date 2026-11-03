// Raw ↔ mg/dL, ported from LoopKit/LibreTransmitter
// (MeasurementProtocol.glucoseValueFromRaw in
// LibreSensor/GlucoseAlgorithm/GlucoseFromRaw.swift and Measurement(bytes:) in
// LibreSensor/SensorContents/Measurement.swift @ 4110512), MIT License,
// Copyright (c) 2016 Mark Wilson and contributors.

package libre2

import "math"

// Calibration is what LibreTransmitter needs, besides a raw value, to compute
// glucose: SensorData.calibrationData's i1…i6, read from the FRAM, and the
// temperature fields that come with every measurement.
//
// I1…I6 hold the values calibrationData returns (I3 signed, I5 and I6 already
// shifted left by 2), not the bits. The sim keeps the FRAM template's
// calibration bits as they are, so these always equal calibrationData(the
// template); NewState decodes them from there.
//
// RawTemperature and RawTemperatureAdjustment are the sim's fixed
// temperature: record 0 of Libre2.BLEExample, decrypted, read with
// PreLibre2.parseBLEData's bit layout (temperature = bits 14…25 << 2,
// adjustment = bits 26…30 << 2, sign = bit 31): 6336 and +8. With Example2's
// I6 they give a skin temperature of about 31.6 °C. Every record the sim
// writes, in FRAM and (M4) over BLE, carries these two values, so the
// temperature term is constant and rawForGlucose can invert exactly.
type Calibration struct {
	I1 int `json:"i1"`
	I2 int `json:"i2"`
	I3 int `json:"i3"`
	I4 int `json:"i4"`
	I5 int `json:"i5"`
	I6 int `json:"i6"`

	RawTemperature           int `json:"rawTemperature"`
	RawTemperatureAdjustment int `json:"rawTemperatureAdjustment"`
}

// info is the calibration as LibreTransmitter's SensorData.CalibrationInfo
// has it: i1…i6 only. The temperature fields belong to each measurement, not
// to the calibration; they are kept in Calibration (and its saved JSON) only
// because the inverse needs them next to i1…i6.
func (c Calibration) info() map[string]int {
	return map[string]int{"i1": c.I1, "i2": c.I2, "i3": c.I3, "i4": c.I4, "i5": c.I5, "i6": c.I6}
}

// measurementTemperature is the temperature records carry from the next
// minute on: the raw fields and the °C glucoseValueFromRaw makes of them, the
// tissue's latest temperature and how far it lags the skin.
func (s *State) measurementTemperature() map[string]interface{} {
	c := s.Calibration
	raw, lag := c.RawTemperature, 0.0
	if t := s.Temperature; t != nil {
		raw, lag = c.rawTemperatureFor(t.Celsius), t.TissueLagMinutes
	}
	celsius := c.temperatureFor(raw, c.RawTemperatureAdjustment)
	tissue := celsius
	if n := len(s.TissueCelsius); n > 0 {
		tissue = s.TissueCelsius[n-1]
	}
	return map[string]interface{}{"raw": raw, "adjustment": c.RawTemperatureAdjustment,
		"celsius": math.Round(celsius*10) / 10, "tissueCelsius": math.Round(tissue*10) / 10, "tissueLagMinutes": lag}
}

// rawTemperatureFor is the raw temperature field (a multiple of 4, as records
// hold it >> 2) whose temperature is nearest celsius, with the calibration's
// adjustment. The thermistor's temperature falls as the raw value rises.
func (c Calibration) rawTemperatureFor(celsius float64) int {
	lo, hi := 1, maxRawTemperature/4 // in units of 4
	for lo < hi {
		mid := (lo + hi) / 2
		if c.temperatureFor(mid*4, c.RawTemperatureAdjustment) > celsius {
			lo = mid + 1
		} else {
			hi = mid
		}
	}
	best := lo
	if lo > 1 && math.Abs(c.temperatureFor((lo-1)*4, c.RawTemperatureAdjustment)-celsius) <
		math.Abs(c.temperatureFor(lo*4, c.RawTemperatureAdjustment)-celsius) {
		best = lo - 1
	}
	return best * 4
}

// maxRawTemperature is the largest raw temperature a record holds (12 bits << 2).
const maxRawTemperature = (1<<12 - 1) << 2

// The temperature fields of BLEExample's first record (see Calibration).
const (
	defaultRawTemperature           = 6336
	defaultRawTemperatureAdjustment = 8
)

// maxRaw is the largest raw glucose a record holds (14 bits).
const maxRaw = 1<<14 - 1

// temperature is glucoseValueFromRaw's thermistor model: the sensor's
// temperature in °C for the calibration's fixed temperature fields.
func (c Calibration) temperature() float64 {
	return c.temperatureFor(c.RawTemperature, c.RawTemperatureAdjustment)
}

// temperatureFor is the thermistor model for a record's temperature fields.
func (c Calibration) temperatureFor(rawTemperature, rawTemperatureAdjustment int) float64 {
	const (
		x  = 1000 + 71500
		y  = 1000
		ca = 0.0009180023
		cb = 0.0001964561
		cc = 0.0000007061775
		cd = 0.00000005283566
	)
	r := float64(rawTemperature)*x/(float64(rawTemperatureAdjustment)+float64(c.I6)) - y
	logR := math.Log(r)
	d := math.Pow(logR, 3)*cd + math.Pow(logR, 2)*cc + logR*cb + ca
	return 1/d - 273.15
}

// mgdlForRaw is glucoseValueFromRaw: the unrounded mg/dL LibreTransmitter
// computes for raw (before extraSlope/extraOffset, which default to 1 and 0).
// Like the Swift, it indexes t1 and t2 with I2-1, so I2 must be in 1…1023.
func (c Calibration) mgdlForRaw(raw int) float64 { return c.mgdlForRawAt(raw, c.temperature()) }

// mgdlForRawAt is glucoseValueFromRaw at a sensor temperature in °C.
func (c Calibration) mgdlForRawAt(raw int, celsius float64) float64 {
	g1 := 65.0 * (float64(raw) - float64(c.I3)) / (float64(c.I4) - float64(c.I3))
	g2 := math.Pow(1.045, 32.5-celsius)
	g3 := g1 * g2
	v1 := t1[c.I2-1]
	v2 := t2[c.I2-1]
	return (g3 - v1) / v2
}

// rawForGlucose inverts mgdlForRaw in closed form. With the temperature fixed,
// mgdl = (65·(raw−i3)/(i4−i3)·g2 − v1)/v2 is linear in raw, so
//
//	raw = i3 + (i4−i3)/65 · (mgdl·v2 + v1) / g2
//
// rounded and held to 1…16383. 0 stays 0: no reading (warmup), which the app
// skips.
func (c Calibration) rawForGlucose(mgdl uint16) uint16 {
	return c.rawForGlucoseAt(mgdl, c.temperature())
}

// rawForGlucoseAt is rawForGlucose at a sensor temperature in °C.
func (c Calibration) rawForGlucoseAt(mgdl uint16, celsius float64) uint16 {
	if mgdl == 0 {
		return 0
	}
	g2 := math.Pow(1.045, 32.5-celsius)
	v1 := t1[c.I2-1]
	v2 := t2[c.I2-1]
	raw := float64(c.I3) + (float64(c.I4)-float64(c.I3))/65*(float64(mgdl)*v2+v1)/g2
	raw = math.Round(raw)
	if raw < 1 {
		return 1
	}
	if raw > maxRaw {
		return maxRaw
	}
	return uint16(raw)
}

// readBits is SensorData.readBits (and PreLibre2.swift's private copy):
// bitCount bits, LSB first, starting bitOffset bits into buffer[byteOffset].
func readBits(buffer []byte, byteOffset, bitOffset, bitCount int) int {
	res := 0
	for i := 0; i < bitCount; i++ {
		t := byteOffset*8 + bitOffset + i
		if t >= 0 && (buffer[t/8]>>(t%8))&1 == 1 {
			res |= 1 << i
		}
	}
	return res
}

// writeBits is SensorData.writeBits, in place: the low bitCount bits of
// value, LSB first.
func writeBits(buffer []byte, byteOffset, bitOffset, bitCount, value int) {
	for i := 0; i < bitCount; i++ {
		t := byteOffset*8 + bitOffset + i
		buffer[t/8] = buffer[t/8]&^(1<<(t%8)) | byte((value>>i)&1)<<(t%8)
	}
}

// measurementSize is the size of one trend or history record in the FRAM.
const measurementSize = 6

// putMeasurement writes a 6-byte FRAM record in Measurement(bytes:)'s layout:
// raw glucose bits 0…13, error bits 14…25 (0: no error), raw temperature >> 2
// in bits 26…37, |adjustment| >> 2 in bits 38…46, adjustment sign in bit 47.
func putMeasurement(b []byte, raw uint16, rawTemperature, rawTemperatureAdjustment int) {
	for i := range b[:measurementSize] {
		b[i] = 0
	}
	writeBits(b, 0, 0, 0xe, int(raw))
	writeBits(b, 0, 0x1a, 0xc, rawTemperature>>2)
	adj, neg := rawTemperatureAdjustment, 0
	if adj < 0 {
		adj, neg = -adj, 1
	}
	writeBits(b, 0, 0x26, 0x9, adj>>2)
	writeBits(b, 0, 0x2f, 0x1, neg)
}

// measurement is what Measurement(bytes:) reads from a 6-byte record.
type measurement struct {
	RawGlucose, RawTemperature, RawTemperatureAdjustment, Error int
}

// parseMeasurement is Measurement(bytes:).
func parseMeasurement(b []byte) measurement {
	m := measurement{
		RawGlucose:               readBits(b, 0, 0, 0xe),
		RawTemperature:           readBits(b, 0, 0x1a, 0xc) << 2,
		RawTemperatureAdjustment: readBits(b, 0, 0x26, 0x9) << 2,
		Error:                    readBits(b, 0, 0xe, 0xc),
	}
	if readBits(b, 0, 0x2f, 0x1) != 0 {
		m.RawTemperatureAdjustment = -m.RawTemperatureAdjustment
	}
	return m
}
