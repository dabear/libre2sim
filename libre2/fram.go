// FRAM encryption, ported from LoopKit/LibreTransmitter
// (Libre2.decryptFRAM in LibreSensor/SensorContents/PreLibre2.swift @ 4110512),
// MIT License, Copyright (c) 2016 Mark Wilson and contributors.

package libre2

import (
	"encoding/binary"
	"time"
)

const (
	framBlocks = 43
	framSize   = framBlocks * 8 // 344
)

// cryptFRAM XORs the 43-block FRAM image with the .libre2 keystream. XOR makes
// it its own inverse: it decrypts what the sensor returns and encrypts what the
// emulator serves. Block i's key is
// processCrypto(prepareVariables(uid, i, infoWord(patchInfo) ^ 0x44)).
func cryptFRAM(uid, patchInfo, fram []byte) []byte {
	y := infoWord(patchInfo) ^ 0x44
	out := make([]byte, 0, framSize)
	ks := make([]byte, 0, 8)
	for i := 0; i < framBlocks; i++ {
		ks = keyBytes(ks[:0], processCrypto(prepareVariables(uid, uint16(i), y)))
		for j := 0; j < 8; j++ {
			out = append(out, fram[i*8+j]^ks[j])
		}
	}
	return out
}

// FRAM regions, as SensorData slices them.
const (
	headerStart = 0
	bodyStart   = 24
	footerStart = 320
)

// FRAM state bytes (header[4]), as LibreTransmitter's SensorState names them.
// SensorState has 0x04 .expired as well: a real sensor's grace period after
// its wear time, when it still answers but no longer measures. The sim has no
// grace period (sim's choice): a sensor whose wear time is up shuts down at
// once, like one that was terminated, and both report 0x05 .shutdown.
const (
	framNotActivated = 0x01 // .notYetStarted
	framWarmup       = 0x02 // .starting
	framReady        = 0x03 // .ready
	framShutdown     = 0x05 // .shutdown: worn out or terminated
)

// framTemplate is Libre2.Example2 from PreLibre2.swift, decrypted (with
// uid df20be0000a407e0 and patch info 9d0830017625). All three of its CRCs
// check. plainFRAM starts from it and overwrites only the fields
// LibreTransmitter reads, so every byte nobody understands stays as a real
// sensor wrote it, and the sensor's calibration (bits at byte 2 and 0x150)
// is that sensor's. FakeSensorPairingData's xDrip sample would be the other
// candidate, but it does not decrypt to valid CRCs with the patch info it
// ships with (see TestFRAMXDripSample).
var framTemplate = hexBytes(
	"815a101c030000000000000000000000000000000000000008da0213480500849600430500b896008305005c96008305" +
		"005c96007505006496006d05005c960072050068d6007d05005c96007705005096006b05004096006105003896006905" +
		"083696006505003496005105002496004b05003496004e0500509600c30700849600210800d4d500f10600d496006f05" +
		"00ec9600890500149900950500c89800d505088698007106083298009b06008c5800d00600585800a80600249800b605" +
		"002898001705000c9800f10400dc97005805003458009d05005897008f0500d496008a05008c96005805003496000e04" +
		"089e5800c303000098007a0400189700f80400c89700420400409800150400649800710400fc97007104005097002f04" +
		"083e58008604082a9800c10408c657009e06006097002c0700309700620c010071c63001ca091751140396805a00cda6" +
		"18a31a0000b2c860")

// Ring buffers in the body, as SensorData.trendMeasurements and
// historyMeasurements read them.
const (
	trendRecords   = 16  // one a minute
	trendOffset    = 4   // body offset of the trend ring
	historyRecords = 32  // one every 15 minutes
	historyOffset  = 100 // body offset of the history ring
	minutesOffset  = 292 // body offset of minutesSinceStart, LE2
	wearOffset     = 6   // footer offset of maxMinutesWearTime, LE2
)

// framState is header[4] at now.
func (s *State) framState(now time.Time) byte {
	switch {
	case s.Activated == nil:
		return framNotActivated
	case s.Terminated != nil || s.lifeCount(now) >= s.WearMinutes:
		return framShutdown
	case s.lifeCount(now) < s.WarmupMinutes:
		return framWarmup
	default:
		return framReady
	}
}

// framMinutes is minutesSinceStart at now. A sensor that has shut down stops
// counting (sim's choice): at its wear time, or where it was terminated.
func (s *State) framMinutes(now time.Time) int {
	lc := s.lifeCount(now)
	if s.Terminated != nil && int(s.Terminated.LifeCount) < lc {
		lc = int(s.Terminated.LifeCount)
	}
	if lc > s.WearMinutes {
		lc = s.WearMinutes
	}
	return lc
}

// plainFRAM is the sensor's 344-byte FRAM at now, unencrypted: framTemplate
// with the state byte, ring indices, trend and history records, minutes since
// start and wear time filled in, and all three CRCs recomputed.
//
// Ring layout (sim's choice, consistent with how SensorData reads it): the
// record for minute m sits in trend slot (m-1) mod 16 and body[2] =
// minutes mod 16, so the newest trend record, the one just before body[2], is
// the current minute's. The history ring holds minute 15·j in slot
// (j-1) mod 32, with body[3] = ((minutes-3)/15) mod 32, which is the
// "in sync" case of SensorData.dateOfMostRecentHistoryValue: the newest
// history record is 3 to 17 minutes old. Slots for minutes before the sensor
// started are all zeros; minutes without a reading (warmup) have raw 0.
func (s *State) plainFRAM(now time.Time) []byte {
	f := append([]byte(nil), framTemplate...)
	header, body, footer := f[headerStart:bodyStart], f[bodyStart:footerStart], f[footerStart:]

	header[4] = s.framState(now)

	lc := 0
	if s.Activated != nil {
		lc = s.framMinutes(now)
		s.fill(lc)
	}
	record := func(dst []byte, minute int) {
		if s.Activated == nil || minute < 0 {
			for i := range dst[:measurementSize] {
				dst[i] = 0
			}
			return
		}
		raw, t, adj := s.record(minute)
		putMeasurement(dst, raw, t, adj)
	}

	body[2] = byte(lc % trendRecords)
	for k := 0; k < trendRecords; k++ {
		m := lc - k
		slot := mod(m-1, trendRecords)
		record(body[trendOffset+slot*measurementSize:], m)
	}

	q := 0 // history records so far: minutes 15, 30, …, 15·q
	if lc >= 3 {
		q = (lc - 3) / 15
	}
	body[3] = byte(q % historyRecords)
	for k := 0; k < historyRecords; k++ {
		j := q - k
		slot := mod(j-1, historyRecords)
		m := 15 * j
		if j < 1 {
			m = -1
		}
		record(body[historyOffset+slot*measurementSize:], m)
	}

	binary.LittleEndian.PutUint16(body[minutesOffset:], uint16(lc))
	binary.LittleEndian.PutUint16(footer[wearOffset:], uint16(s.WearMinutes))

	setFramRegionCRC(header)
	setFramRegionCRC(body)
	setFramRegionCRC(footer)
	return f
}

// encryptedFRAM is the FRAM as the sensor serves it over NFC.
func (s *State) encryptedFRAM(now time.Time) []byte {
	return cryptFRAM(s.uid(), s.patchInfo(), s.plainFRAM(now))
}

// DecryptFRAM is Libre2.decryptFRAM for a .libre2 sensor: it turns the 344
// bytes read over NFC back into plain FRAM. sensorUID is in sensorUID order.
func DecryptFRAM(sensorUID, patchInfo, fram []byte) []byte {
	return cryptFRAM(sensorUID, patchInfo, fram)
}

// calibrationData is SensorData.calibrationData: i1…i6 from the FRAM's bits
// at byte 2 and byte 0x150. The temperature fields are left zero.
func calibrationData(fram []byte) Calibration {
	i3 := readBits(fram, 0x150, 0, 8)
	if readBits(fram, 0x150, 0x21, 1) != 0 {
		i3 = -i3
	}
	return Calibration{
		I1: readBits(fram, 2, 0, 3),
		I2: readBits(fram, 2, 3, 0xa),
		I3: i3,
		I4: readBits(fram, 0x150, 8, 0xe),
		I5: readBits(fram, 0x150, 0x28, 0xc) << 2,
		I6: readBits(fram, 0x150, 0x34, 0xc) << 2,
	}
}

func mod(a, n int) int { return (a%n + n) % n }
