// Streaming enable and unlock, ported from LoopKit/LibreTransmitter
// (Libre2.streamingUnlockPayload in LibreSensor/SensorContents/PreLibre2.swift
// and nfcCommand in LibreSensor/SensorPairing/SensorPairingService.swift
// @ 4110512), MIT License, Copyright (c) 2016 Mark Wilson and contributors.

package libre2

// subcmdEnableStreaming is Subcommand.enableStreaming, the first parameter byte
// of NFC custom command 0xA1 that turns on BLE.
const subcmdEnableStreaming = 0x1e

func le4(v uint32) [4]byte { return [4]byte{byte(v), byte(v >> 8), byte(v >> 16), byte(v >> 24)} }

// enableStreamingD is d in SensorPairingService.nfcCommand(.enableStreaming):
// usefulFunction(uid, 0x1E, UInt16(patchInfo[4...5]) ^ UInt16(b[1], b[0])),
// where b = code little-endian. UInt16(patchInfo[4...5]) is the Data
// initialiser, data[1]<<8 + data[0], i.e. infoWord.
func enableStreamingD(uid, patchInfo []byte, code uint32) [4]byte {
	b := le4(code)
	y := infoWord(patchInfo) ^ word(b[1], b[0])
	return usefulFunction(uid, subcmdEnableStreaming, y)
}

// enableStreamingParams is the full parameter block of the 0xA1 command:
// [0x1E, code LE4, d4].
func enableStreamingParams(uid, patchInfo []byte, code uint32) []byte {
	b := le4(code)
	d := enableStreamingD(uid, patchInfo, code)
	p := []byte{subcmdEnableStreaming}
	p = append(p, b[:]...)
	return append(p, d[:]...)
}

// unlockPayload is Libre2.streamingUnlockPayload: the 12 bytes the app writes
// to F001, time LE4 ‖ 8 key bytes, with time = enableTime + count.
func unlockPayload(uid, patchInfo []byte, enableTime uint32, count uint16) [12]byte {
	b := le4(enableTime + uint32(count))

	ad := usefulFunction(uid, 0x1b, 0x1b6a)
	ed := usefulFunction(uid, 0x1e, uint16(enableTime&0xFFFF)^infoWord(patchInfo))

	t11 := word(ed[1], ed[0]) ^ word(b[3], b[2])
	t12 := word(ad[1], ad[0])
	t13 := word(ed[3], ed[2]) ^ word(b[1], b[0])
	t14 := word(ad[3], ad[2])

	t2 := processCrypto(prepareVariables4(uid, t11, t12, t13, t14))

	// PreLibre2.swift's private crc16 (== libreCRC16), then .byteSwapped.
	crc := func(p ...byte) uint16 {
		c := libreCRC16(p)
		return c>>8 | c<<8
	}
	t31 := crc(0xc1, 0xc4, 0xc3, 0xc0, 0xd4, 0xe1, 0xe7, 0xba, byte(t2[0]), byte(t2[0]>>8))
	t32 := crc(byte(t2[1]), byte(t2[1]>>8), byte(t2[2]), byte(t2[2]>>8), byte(t2[3]), byte(t2[3]>>8))
	t33 := crc(ad[0], ad[1], ad[2], ad[3], ed[0], ed[1])
	t34 := crc(ed[2], ed[3], b[0], b[1], b[2], b[3])

	t4 := processCrypto(prepareVariables4(uid, t31, t32, t33, t34))

	var out [12]byte
	copy(out[:4], b[:])
	copy(out[4:], keyBytes(nil, t4))
	return out
}
