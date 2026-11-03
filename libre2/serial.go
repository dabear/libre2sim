// Serial numbers, ported from LoopKit/LibreTransmitter
// (LibreSensor/SensorContents/SensorSerialNumber.swift @ 4110512), MIT
// License, Copyright (c) 2016 Mark Wilson and contributors; originally by Uwe
// Petersen.

package libre2

import "strconv"

// serialAlphabet is SensorSerialNumber's lookupTable: 0-9 and A-Z without B,
// I, O and S.
const serialAlphabet = "0123456789ACDEFGHJKLMNPQRTUVWXYZ"

// SerialNumber is SensorSerialNumber.serialNumber, for display only: the
// family digit (SensorFamily raw value, patchInfo[2] >> 4; 3 for a Libre 2),
// then the 48 bits after e0 07 of the UID read as ten 5-bit groups, the last
// one padded with two zero bits. sensorUID is in sensorUID order (… a4 07 e0).
func SerialNumber(sensorUID []byte, family int) string {
	if len(sensorUID) != 8 {
		return "invalid uid"
	}
	// uid.reversed().suffix(6)
	var b [6]byte
	for i := range b {
		b[i] = sensorUID[5-i]
	}
	// Swift's << on UInt8 drops the high bits, as Go's byte shifts do; the
	// sums never carry because the two halves never overlap.
	five := [10]byte{
		b[0] >> 3,
		b[0]<<2 + b[1]>>6,
		b[1] >> 1,
		b[1]<<4 + b[2]>>4,
		b[2]<<1 + b[3]>>7,
		b[3] >> 2,
		b[3]<<3 + b[4]>>5,
		b[4],
		b[5] >> 3,
		b[5] << 2,
	}
	out := []byte(strconv.Itoa(family))
	for _, v := range five {
		out = append(out, serialAlphabet[v&0x1f])
	}
	return string(out)
}
