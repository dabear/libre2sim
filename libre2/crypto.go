// Libre 2 crypto, ported line for line from LoopKit/LibreTransmitter
// (LibreSensor/SensorContents/PreLibre2.swift @ 4110512), MIT License,
// Copyright (c) 2016 Mark Wilson and contributors. That code in turn builds on
// the LibreDirect and xDrip work.
//
// UID convention: every uid here is LibreTransmitter's sensorUID, i.e. the NFC
// tag identifier reversed (it ends ... a4 07 e0).

package libre2

// key is Libre2.key.
var key = [4]uint16{0xA0C5, 0x6860, 0x0000, 0x14C6}

// word is LibreTransmitter's UInt16(high, low) initialiser.
func word(high, low byte) uint16 { return uint16(high)<<8 | uint16(low) }

// processCrypto is Libre2.processCrypto.
func processCrypto(in [4]uint16) [4]uint16 {
	op := func(v uint16) uint16 {
		res := v >> 2
		if v&1 != 0 {
			res ^= key[1]
		}
		if v&2 != 0 {
			res ^= key[0]
		}
		return res
	}

	r0 := op(in[0]) ^ in[3]
	r1 := op(r0) ^ in[2]
	r2 := op(r1) ^ in[1]
	r3 := op(r2) ^ in[0]
	r4 := op(r3)
	r5 := op(r4 ^ r0)
	r6 := op(r5 ^ r1)
	r7 := op(r6 ^ r2)

	f1 := r0 ^ r4
	f2 := r1 ^ r5
	f3 := r2 ^ r6
	f4 := r3 ^ r7

	return [4]uint16{f4, f3, f2, f1}
}

// prepareVariables is Libre2.prepareVariables(id:x:y:) (identical to the
// sensorUID:x:y: variant). Swift sums in UInt and truncates to 16 bits, which
// is the same as uint32 arithmetic truncated here.
func prepareVariables(uid []byte, x, y uint16) [4]uint16 {
	s1 := uint16(uint32(word(uid[5], uid[4])) + uint32(x) + uint32(y))
	s2 := uint16(uint32(word(uid[3], uid[2])) + uint32(key[2]))
	s3 := uint16(uint32(word(uid[1], uid[0])) + uint32(x)*2)
	s4 := uint16(0x241a) ^ key[3]
	return [4]uint16{s1, s2, s3, s4}
}

// prepareVariables4 is Libre2.prepareVariables(sensorUID:i1:i2:i3:i4:).
func prepareVariables4(uid []byte, i1, i2, i3, i4 uint16) [4]uint16 {
	s1 := uint16(uint32(word(uid[5], uid[4])) + uint32(i1))
	s2 := uint16(uint32(word(uid[3], uid[2])) + uint32(i2))
	s3 := uint16(uint32(word(uid[1], uid[0])) + uint32(i3) + uint32(key[2]))
	s4 := uint16(uint32(i4) + uint32(key[3]))
	return [4]uint16{s1, s2, s3, s4}
}

// usefulFunction is Libre2.usefulFunction (both variants compute the same).
func usefulFunction(uid []byte, x, y uint16) [4]byte {
	k := processCrypto(prepareVariables(uid, x, y))
	r1 := k[0] ^ 0x4163
	r2 := k[1] ^ 0x4344
	return [4]byte{byte(r1), byte(r1 >> 8), byte(r2), byte(r2 >> 8)}
}

// infoWord is UInt16(info[5], info[4]): patch info bytes 4-5, little-endian.
func infoWord(patchInfo []byte) uint16 { return word(patchInfo[5], patchInfo[4]) }

// keyBytes appends the four words of k, each little-endian, as Swift does when
// it turns a block key into bytes.
func keyBytes(dst []byte, k [4]uint16) []byte {
	for _, w := range k {
		dst = append(dst, byte(w), byte(w>>8))
	}
	return dst
}
