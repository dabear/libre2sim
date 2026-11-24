// SPDX-License-Identifier: GPL-3.0-or-later
//
// FreeStyle Libre 2 "Gen2" crypto, ported from Juggluco
// (Common/src/main/cpp/libre2_imp/gen2/protocol.c and protocol.h, the
// authoritative open-source reference, GPL-3.0, Copyright (c) Jaap Korthals
// Altes). The algorithms (Speck64/128, Speck-CTR, Speck-CMAC and the CMAC-KDF),
// the root tables, the KDF labels and the nonce constructions are transcribed
// faithfully from that source.
//
// Terminology note: the reference names these functions l2g2_*; this port keeps
// the names used by the simulator. The simulator plays the SENSOR, so it both
// decrypts (host -> sensor) and encrypts (sensor -> host) BLE data frames.

package libre2

import (
	"encoding/binary"
	"fmt"
	"math/bits"
)

// --- Speck 64/128 -----------------------------------------------------------

const (
	speckBlockSize = 8  // 64-bit block
	speckKeySize   = 16 // 128-bit key
	speckRounds    = 27
)

// speckKey holds the expanded round keys of a Speck 64/128 schedule.
type speckKey [speckRounds]uint32

// newSpeckKey expands a 16-byte key into 27 round keys. The key schedule reads
// four little-endian 32-bit words (protocol.c's cur/old/mid/new) and runs the
// Speck round function with the round index as the constant: with x the l
// register and y the running key word,
//
//	l_next = (ror(l, 8) + k) ^ i
//	k_next = rol(k, 3) ^ l_next
func newSpeckKey(key []byte) speckKey {
	cur := binary.LittleEndian.Uint32(key[0:]) // k[0]
	old := binary.LittleEndian.Uint32(key[4:]) // l[0]
	mid := binary.LittleEndian.Uint32(key[8:]) // l[1]
	nw := binary.LittleEndian.Uint32(key[12:]) // l[2]
	var rk speckKey
	rk[0] = cur
	for i := uint32(0); i < speckRounds-1; i++ {
		t := (bits.RotateLeft32(old, -8) + cur) ^ i
		cur = bits.RotateLeft32(cur, 3) ^ t
		rk[i+1] = cur
		old, mid, nw = mid, nw, t
	}
	return rk
}

// encrypt encrypts one 8-byte block. Block I/O is little-endian: right is the
// low word at in[0:4], left the high word at in[4:8], and the output is
// le(right) ‖ le(left). The round function is x = (ror(x,8) + y) ^ k;
// y = rol(y,3) ^ x, with x = left and y = right.
func (rk *speckKey) encrypt(in []byte) [speckBlockSize]byte {
	y := binary.LittleEndian.Uint32(in[0:]) // right
	x := binary.LittleEndian.Uint32(in[4:]) // left
	for i := 0; i < speckRounds; i++ {
		x = (bits.RotateLeft32(x, -8) + y) ^ rk[i]
		y = bits.RotateLeft32(y, 3) ^ x
	}
	var out [speckBlockSize]byte
	binary.LittleEndian.PutUint32(out[0:], y)
	binary.LittleEndian.PutUint32(out[4:], x)
	return out
}

// --- Speck-CTR --------------------------------------------------------------

// cryptCTR XORs data in place with the Speck keystream in CTR mode: each 8-byte
// block of keystream is Speck_K(nonce), and after each block only nonce[0] is
// incremented, with no carry into nonce[1] (protocol.c's crypt_ctr).
func cryptCTR(rk *speckKey, nonce [speckBlockSize]byte, data []byte) {
	for off := 0; off < len(data); off += speckBlockSize {
		ks := rk.encrypt(nonce[:])
		n := len(data) - off
		if n > speckBlockSize {
			n = speckBlockSize
		}
		for i := 0; i < n; i++ {
			data[off+i] ^= ks[i]
		}
		nonce[0]++ // byte-0 counter, no carry
	}
}

// --- Speck-CMAC -------------------------------------------------------------

// cmacReduction is the CMAC subkey reduction constant for a 64-bit block.
const cmacReduction = 0x1b

// cmacDouble doubles a block in GF(2^64): a left shift by one, and if the top
// bit was set, XOR the reduction constant into the last byte.
func cmacDouble(in [speckBlockSize]byte) [speckBlockSize]byte {
	carry := in[0]&0x80 != 0
	var out [speckBlockSize]byte
	for i := 0; i < speckBlockSize; i++ {
		out[i] = in[i] << 1
		if i+1 < speckBlockSize && in[i+1]&0x80 != 0 {
			out[i] |= 1
		}
	}
	if carry {
		out[speckBlockSize-1] ^= cmacReduction
	}
	return out
}

// speckCMAC computes the CMAC of msg under the Speck block cipher: the subkey
// L = Speck_K(0^8), K1 = double(L), K2 = double(K1); the message is processed in
// 8-byte blocks, the last block XORed with K1 if it is full, or padded with
// 0x80 and XORed with K2 otherwise (protocol.c's CMAC).
func speckCMAC(rk *speckKey, msg []byte) [speckBlockSize]byte {
	l := rk.encrypt(make([]byte, speckBlockSize))
	k1 := cmacDouble(l)
	k2 := cmacDouble(k1)

	nblocks := (len(msg) + speckBlockSize - 1) / speckBlockSize
	if nblocks == 0 {
		nblocks = 1
	}

	var mac [speckBlockSize]byte
	for b := 0; b < nblocks-1; b++ {
		for i := 0; i < speckBlockSize; i++ {
			mac[i] ^= msg[b*speckBlockSize+i]
		}
		mac = rk.encrypt(mac[:])
	}

	var last [speckBlockSize]byte
	rem := len(msg) - (nblocks-1)*speckBlockSize
	copy(last[:], msg[(nblocks-1)*speckBlockSize:])
	if rem == speckBlockSize {
		for i := 0; i < speckBlockSize; i++ {
			last[i] ^= k1[i]
		}
	} else {
		last[rem] = 0x80
		for i := 0; i < speckBlockSize; i++ {
			last[i] ^= k2[i]
		}
	}
	for i := 0; i < speckBlockSize; i++ {
		mac[i] ^= last[i]
	}
	return rk.encrypt(mac[:])
}

// --- CMAC-KDF ---------------------------------------------------------------

// deriveKey is protocol.c's key-derivation function: a 16-byte key built from
// two CMAC blocks, CMAC(counter=1) ‖ CMAC(counter=2). Each CMAC is taken over
// counter(1 byte) ‖ label(17 bytes) ‖ context ‖ outputBits{0x80, 0x00}.
func deriveKey(root, label, context []byte) [speckKeySize]byte {
	rk := newSpeckKey(root)
	var out [speckKeySize]byte
	for counter := byte(1); counter <= 2; counter++ {
		msg := make([]byte, 0, 1+len(label)+len(context)+2)
		msg = append(msg, counter)
		msg = append(msg, label...)
		msg = append(msg, context...)
		msg = append(msg, 0x80, 0x00)
		mac := speckCMAC(&rk, msg)
		copy(out[int(counter-1)*speckBlockSize:], mac[:])
	}
	return out
}

// --- Root tables and labels (verbatim from protocol.c) ----------------------

// sensorRoots are the per-slot roots used to derive the sensor key.
var sensorRoots = [2][speckKeySize]byte{
	{0xa1, 0xaa, 0x73, 0x8f, 0x0c, 0xf7, 0x58, 0x01, 0xa1, 0x6d, 0xd4, 0xf7, 0x90, 0x6e, 0x97, 0x2a},
	{0x3c, 0xe8, 0xbd, 0xbb, 0x8c, 0xb3, 0x0a, 0x48, 0xa7, 0xd6, 0x93, 0xf7, 0xb7, 0x2e, 0xec, 0x6c},
}

// sessionRoots are the per-slot roots used to derive the session key.
var sessionRoots = [2][speckKeySize]byte{
	{0xe8, 0x2f, 0x8f, 0xc3, 0x21, 0x99, 0x56, 0x3c, 0xfd, 0xa8, 0x0c, 0x7f, 0x6c, 0x2a, 0x67, 0x21},
	{0x4e, 0x28, 0x73, 0xbd, 0x16, 0xc5, 0x4a, 0xdc, 0xe0, 0x38, 0xea, 0xd5, 0xb2, 0x5b, 0x68, 0xa3},
}

// KDF labels, 17 bytes each including the trailing NUL. The session label has a
// capital K ("SpecK") where the sensor label has a lowercase k ("Speck").
var (
	sensorLabel  = []byte("ADC_*SpeckPatch#\x00")
	sessionLabel = []byte("ADC_*SpecKpatch#\x00")
)

// DefaultRootSlot is the root slot used unless a sensor selects another.
const DefaultRootSlot = 1

// deriveSensorKey derives the 16-byte sensor key for a root slot (1 or 2) from
// the UID, using all but the last two bytes of the UID as context.
func deriveSensorKey(slot int, uid []byte) ([speckKeySize]byte, error) {
	if slot != 1 && slot != 2 {
		return [speckKeySize]byte{}, fmt.Errorf("libre2: unsupported root slot %d", slot)
	}
	if len(uid) < 2 {
		return [speckKeySize]byte{}, fmt.Errorf("libre2: UID is %d bytes, too short", len(uid))
	}
	root := sensorRoots[slot-1]
	return deriveKey(root[:], sensorLabel, uid[:len(uid)-2]), nil
}

// deriveSessionKey derives the 16-byte session key for a root slot (1 or 2)
// from a 16-byte context.
func deriveSessionKey(slot int, context []byte) ([speckKeySize]byte, error) {
	if slot != 1 && slot != 2 {
		return [speckKeySize]byte{}, fmt.Errorf("libre2: unsupported root slot %d", slot)
	}
	root := sessionRoots[slot-1]
	return deriveKey(root[:], sessionLabel, context), nil
}

// --- Nonces -----------------------------------------------------------------

// Transport selects the transport tag that goes into an auth nonce.
type Transport int

const (
	TransportNFC Transport = 0 // L2G2_NFC
	TransportBLE Transport = 1 // L2G2_BLE
)

// authNonce builds the authentication nonce from a 3-byte salt, a 32-bit
// counter, the transport and a step added to the counter (protocol.c's
// make_auth_nonce). The BLE transport sets the top bit of nonce[3].
func authNonce(salt [3]byte, counter uint32, transport Transport, step uint32) [speckBlockSize]byte {
	var nonce [speckBlockSize]byte
	nonce[0] = 0
	nonce[1] = salt[0]
	nonce[2] = salt[1]
	nonce[3] = salt[2] & 0x7f
	if transport == TransportBLE {
		nonce[3] |= 0x80
	}
	binary.LittleEndian.PutUint32(nonce[4:], counter+step)
	return nonce
}

// --- BLE data frames --------------------------------------------------------

// bleFrameSize is the size of a Gen2 BLE data frame: a 2-byte plaintext
// sequence number followed by 44 CTR-encrypted payload bytes.
const (
	bleFrameSize   = 46
	bleFramePaySz  = 44 // frame[2:46]
	bleFrameDataSz = 42 // frame[2:44]; frame[44:46] is the CRC
)

// bleFrameNonce builds the CTR nonce for a BLE data frame from the 4-byte
// stream prefix and the frame's 2-byte sequence number (frame[0], frame[1]).
// The top bit of nonce[5] is set.
func bleFrameNonce(streamPrefix [4]byte, seq0, seq1 byte) [speckBlockSize]byte {
	var nonce [speckBlockSize]byte
	copy(nonce[2:6], streamPrefix[:])
	nonce[5] |= 0x80
	nonce[6] = seq0
	nonce[7] = seq1
	return nonce
}

// decryptBLEFrame decrypts a 46-byte Gen2 BLE data frame with the session key.
// The 2-byte sequence number at frame[0:2] stays plaintext; the 44 payload
// bytes are CTR-decrypted. It returns the 46-byte decrypted frame (plaintext
// sequence number followed by the decrypted payload) and fails if the CRC at
// bytes [44:46] does not match the Abbott CRC-16 of bytes [2:44], placed as in
// a Gen1 packet (packetCRCValid), which is what Juggluco's matches_crc reads.
func decryptBLEFrame(sessionKey [speckKeySize]byte, streamPrefix [4]byte, frame []byte) ([]byte, error) {
	if len(frame) != bleFrameSize {
		return nil, fmt.Errorf("libre2: BLE frame is %d bytes, want %d", len(frame), bleFrameSize)
	}
	rk := newSpeckKey(sessionKey[:])
	nonce := bleFrameNonce(streamPrefix, frame[0], frame[1])
	out := make([]byte, bleFrameSize)
	copy(out, frame)
	cryptCTR(&rk, nonce, out[2:])
	if !packetCRCValid(out[2:]) {
		return nil, errPacketCRC
	}
	return out, nil
}

// encryptBLEFrame builds a 46-byte Gen2 BLE data frame. The 2-byte sequence
// number (seq0, seq1) is placed in plaintext at frame[0:2]; the 42 data bytes
// go at frame[2:44]; the Abbott CRC-16 of frame[2:44] is written at
// frame[44:46] as in a Gen1 packet (packetPlain); and the 44 payload bytes are CTR-encrypted with the session
// key. data must be 42 bytes.
func encryptBLEFrame(sessionKey [speckKeySize]byte, streamPrefix [4]byte, seq0, seq1 byte, data []byte) ([]byte, error) {
	if len(data) != bleFrameDataSz {
		return nil, fmt.Errorf("libre2: BLE frame data is %d bytes, want %d", len(data), bleFrameDataSz)
	}
	frame := make([]byte, bleFrameSize)
	frame[0], frame[1] = seq0, seq1
	copy(frame[2:44], data)
	crc := libreCRC16(frame[2:44])
	frame[44], frame[45] = byte(crc>>8), byte(crc)

	rk := newSpeckKey(sessionKey[:])
	nonce := bleFrameNonce(streamPrefix, seq0, seq1)
	cryptCTR(&rk, nonce, frame[2:])
	return frame, nil
}
