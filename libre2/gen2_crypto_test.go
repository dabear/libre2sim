// SPDX-License-Identifier: GPL-3.0-or-later
package libre2

import (
	"bytes"
	"encoding/binary"
	"testing"
)

// TestGen2SpeckKAT checks the Speck 64/128 block cipher against the standard
// test vector (also protocol.c's cipher self-test).
func TestGen2SpeckKAT(t *testing.T) {
	key := []byte{0, 1, 2, 3, 8, 9, 10, 11, 16, 17, 18, 19, 24, 25, 26, 27}
	pt := []byte{0x2d, 0x43, 0x75, 0x74, 0x74, 0x65, 0x72, 0x3b}
	want := [speckBlockSize]byte{0x8b, 0x02, 0x4e, 0x45, 0x48, 0xa5, 0x6f, 0x8c}

	rk := newSpeckKey(key)
	got := rk.encrypt(pt)
	t.Logf("Speck KAT ciphertext: %x", got)
	if got != want {
		t.Fatalf("Speck KAT: got %x, want %x", got, want)
	}
}

// TestGen2CMACDouble checks the GF(2^64) doubling used for the CMAC subkeys,
// including the reduction when the top bit is set.
func TestGen2CMACDouble(t *testing.T) {
	// No overflow: a plain left shift by one.
	in := [speckBlockSize]byte{0x01, 0x02, 0x03, 0x04, 0x05, 0x06, 0x07, 0x08}
	want := [speckBlockSize]byte{0x02, 0x04, 0x06, 0x08, 0x0a, 0x0c, 0x0e, 0x10}
	if got := cmacDouble(in); got != want {
		t.Fatalf("double no-carry: got %x, want %x", got, want)
	}
	// Overflow: shift, then XOR 0x1b into the last byte.
	in = [speckBlockSize]byte{0x80, 0, 0, 0, 0, 0, 0, 0}
	want = [speckBlockSize]byte{0, 0, 0, 0, 0, 0, 0, cmacReduction}
	if got := cmacDouble(in); got != want {
		t.Fatalf("double carry: got %x, want %x", got, want)
	}
}

// TestGen2KDFDeterminism checks that the KDF is deterministic and that the
// derived sensor and session keys are 16 bytes and differ.
func TestGen2KDFDeterminism(t *testing.T) {
	uid := []byte{0x01, 0x02, 0x03, 0x04, 0x05, 0x06, 0x07, 0x08}
	ctx := []byte{0x10, 0x11, 0x12, 0x13, 0x14, 0x15, 0x16, 0x17,
		0x18, 0x19, 0x1a, 0x1b, 0x1c, 0x1d, 0x1e, 0x1f}

	sensor, err := deriveSensorKey(DefaultRootSlot, uid)
	if err != nil {
		t.Fatal(err)
	}
	sensor2, err := deriveSensorKey(DefaultRootSlot, uid)
	if err != nil {
		t.Fatal(err)
	}
	if sensor != sensor2 {
		t.Fatal("deriveSensorKey is not deterministic")
	}
	if len(sensor) != speckKeySize {
		t.Fatalf("sensor key is %d bytes, want %d", len(sensor), speckKeySize)
	}

	session, err := deriveSessionKey(DefaultRootSlot, ctx)
	if err != nil {
		t.Fatal(err)
	}
	session2, err := deriveSessionKey(DefaultRootSlot, ctx)
	if err != nil {
		t.Fatal(err)
	}
	if session != session2 {
		t.Fatal("deriveSessionKey is not deterministic")
	}
	if len(session) != speckKeySize {
		t.Fatalf("session key is %d bytes, want %d", len(session), speckKeySize)
	}

	if sensor == session {
		t.Fatal("sensor and session keys are identical")
	}
	t.Logf("sensor key %x", sensor)
	t.Logf("session key %x", session)

	// Different slots must give different keys; invalid slots must error.
	otherSlot := 2
	s2, err := deriveSensorKey(otherSlot, uid)
	if err != nil {
		t.Fatal(err)
	}
	if s2 == sensor {
		t.Fatal("slots 1 and 2 derive the same sensor key")
	}
	if _, err := deriveSensorKey(0, uid); err == nil {
		t.Fatal("slot 0 should be rejected")
	}
	if _, err := deriveSessionKey(3, ctx); err == nil {
		t.Fatal("slot 3 should be rejected")
	}
}

// TestGen2KDFLabels checks the two KDF labels verbatim: 17 bytes each including
// a trailing NUL, sensor "ADC_*SpeckPatch#" (lowercase k, uppercase P) and
// session "ADC_*SpecKpatch#" (uppercase K, lowercase p).
func TestGen2KDFLabels(t *testing.T) {
	wantSensor := append([]byte("ADC_*SpeckPatch#"), 0)
	wantSession := append([]byte("ADC_*SpecKpatch#"), 0)
	if len(sensorLabel) != 17 || len(sessionLabel) != 17 {
		t.Fatalf("labels are %d/%d bytes, want 17", len(sensorLabel), len(sessionLabel))
	}
	if !bytes.Equal(sensorLabel, wantSensor) {
		t.Fatalf("sensor label %q, want %q", sensorLabel, wantSensor)
	}
	if !bytes.Equal(sessionLabel, wantSession) {
		t.Fatalf("session label %q, want %q", sessionLabel, wantSession)
	}
}

// TestGen2AuthNonce checks the auth-nonce layout and the BLE transport bit.
func TestGen2AuthNonce(t *testing.T) {
	salt := [3]byte{0xaa, 0xbb, 0xff}
	n := authNonce(salt, 0x11223344, TransportNFC, 1)
	if n[0] != 0 || n[1] != 0xaa || n[2] != 0xbb {
		t.Fatalf("nonce prefix %x", n[:4])
	}
	if n[3] != (0xff & 0x7f) { // NFC: top bit clear
		t.Fatalf("NFC nonce[3] = %#x, want %#x", n[3], 0xff&0x7f)
	}
	if got := binary.LittleEndian.Uint32(n[4:]); got != 0x11223344+1 {
		t.Fatalf("counter+step = %#x, want %#x", got, 0x11223344+1)
	}
	b := authNonce(salt, 0x11223344, TransportBLE, 0)
	if b[3]&0x80 == 0 {
		t.Fatalf("BLE nonce[3] = %#x, top bit should be set", b[3])
	}
}

// TestGen2FrameRoundTrip checks that encrypting a BLE data frame and decrypting
// it recovers the plaintext sequence number and data, and that the CRC
// validates. (The simulator is the sensor, so it encrypts.)
func TestGen2FrameRoundTrip(t *testing.T) {
	sessionKey := [speckKeySize]byte{
		0x00, 0x11, 0x22, 0x33, 0x44, 0x55, 0x66, 0x77,
		0x88, 0x99, 0xaa, 0xbb, 0xcc, 0xdd, 0xee, 0xff}
	streamPrefix := [4]byte{0xde, 0xad, 0xbe, 0xef}

	data := make([]byte, bleFrameDataSz)
	for i := range data {
		data[i] = byte(i * 3)
	}

	for _, seq := range [][2]byte{{0, 0}, {0x12, 0x34}, {0xff, 0xff}} {
		frame, err := encryptBLEFrame(sessionKey, streamPrefix, seq[0], seq[1], data)
		if err != nil {
			t.Fatalf("seq %x: encrypt: %v", seq, err)
		}
		if len(frame) != bleFrameSize {
			t.Fatalf("seq %x: frame is %d bytes, want %d", seq, len(frame), bleFrameSize)
		}
		// The sequence number is plaintext at [0:2].
		if frame[0] != seq[0] || frame[1] != seq[1] {
			t.Fatalf("seq %x: plaintext sequence is %x", seq, frame[0:2])
		}

		got, err := decryptBLEFrame(sessionKey, streamPrefix, frame)
		if err != nil {
			t.Fatalf("seq %x: decrypt (CRC) failed: %v", seq, err)
		}
		if got[0] != seq[0] || got[1] != seq[1] {
			t.Fatalf("seq %x: recovered sequence %x", seq, got[0:2])
		}
		if !bytes.Equal(got[2:44], data) {
			t.Fatalf("seq %x: recovered data %x, want %x", seq, got[2:44], data)
		}

		// A corrupted ciphertext byte must fail the CRC.
		frame[10] ^= 1
		if _, err := decryptBLEFrame(sessionKey, streamPrefix, frame); err == nil {
			t.Fatalf("seq %x: corrupted frame passed the CRC", seq)
		}
	}

	// The wrong session key must not decrypt to a valid CRC.
	frame, err := encryptBLEFrame(sessionKey, streamPrefix, 1, 2, data)
	if err != nil {
		t.Fatal(err)
	}
	wrong := sessionKey
	wrong[0] ^= 0xff
	if _, err := decryptBLEFrame(wrong, streamPrefix, frame); err == nil {
		t.Fatal("frame decrypted under the wrong session key")
	}

	// Size guards.
	if _, err := decryptBLEFrame(sessionKey, streamPrefix, frame[:45]); err == nil {
		t.Fatal("a 45-byte frame should be rejected")
	}
	if _, err := encryptBLEFrame(sessionKey, streamPrefix, 0, 0, data[:41]); err == nil {
		t.Fatal("41-byte data should be rejected")
	}
}
