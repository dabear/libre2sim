// FRAM encryption, ported from LoopKit/LibreTransmitter
// (Libre2.decryptFRAM in LibreSensor/SensorContents/PreLibre2.swift @ 4110512),
// MIT License, Copyright (c) 2016 Mark Wilson and contributors.

package libre2

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
