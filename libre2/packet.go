// BLE packet encryption, ported from LoopKit/LibreTransmitter
// (Libre2.decryptBLE in LibreSensor/SensorContents/PreLibre2.swift @ 4110512),
// MIT License, Copyright (c) 2016 Mark Wilson and contributors.

package libre2

import "errors"

const (
	packetSize    = 46 // n0 n1 ‖ ciphertext
	packetPlainSz = 44 // 10 records ‖ age LE2 ‖ CRC BE2
)

// errPacketCRC mirrors decryptBLE's DecryptBLEError.
var errPacketCRC = errors.New("libre2: BLE data decryption failed (CRC)")

// bleKeystream returns the 64-byte keystream for nonce bytes n0 n1 (the first
// two bytes of a packet). Only the first 44 bytes are used.
func bleKeystream(uid []byte, n0, n1 byte) [64]byte {
	d := usefulFunction(uid, 0x1b, 0x1b6a)
	// Swift: UInt16(d[1], d[0]) ^ UInt16(d[3], d[2]) | 0x63. ^ and | share
	// precedence and associate left in Swift (and Go), so this is (a^b)|0x63.
	x := (word(d[1], d[0]) ^ word(d[3], d[2])) | 0x63
	y := word(n1, n0) ^ 0x63

	var ks [64]byte
	k := processCrypto(prepareVariables(uid, x, y))
	for i := 0; i < 8; i++ {
		copy(ks[i*8:], keyBytes(nil, k))
		k = processCrypto(k)
	}
	return ks
}

// decryptPacket is Libre2.decryptBLE: it returns the 44-byte plaintext of a
// 46-byte packet and fails if the plaintext CRC does not check.
func decryptPacket(uid, pkt []byte) ([]byte, error) {
	if len(pkt) != packetSize {
		return nil, errors.New("libre2: packet must be 46 bytes")
	}
	ks := bleKeystream(uid, pkt[0], pkt[1])
	out := make([]byte, packetPlainSz)
	for i := range out {
		out[i] = pkt[2+i] ^ ks[i]
	}
	if !packetCRCValid(out) {
		return nil, errPacketCRC
	}
	return out, nil
}

// encryptPacket builds the 46-byte packet n0 n1 ‖ (pt44 XOR keystream). pt44 is
// used as given; the caller is responsible for its CRC.
func encryptPacket(uid []byte, n0, n1 byte, pt44 []byte) []byte {
	ks := bleKeystream(uid, n0, n1)
	out := make([]byte, 2, packetSize)
	out[0], out[1] = n0, n1
	for i := 0; i < packetPlainSz; i++ {
		out = append(out, pt44[i]^ks[i])
	}
	return out
}
