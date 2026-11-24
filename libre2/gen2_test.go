// SPDX-License-Identifier: GPL-3.0-or-later

package libre2

import (
	"bytes"
	"errors"
	"testing"
	"time"
)

// gen2Client is a minimal host side, written from protocol.c's
// l2g2_build_ble_auth_request / l2g2_accept_auth_response, for unit tests.
// tools/gen2bridge checks the same exchange against Juggluco's own C.
func gen2Client(t *testing.T, g *Gen2Sensor, command byte, hostRandom []byte, challenge []byte) (req []byte, finish func(resp []byte) ([speckKeySize]byte, error)) {
	t.Helper()
	key, err := deriveSensorKey(g.RootSlot, g.UID)
	if err != nil {
		t.Fatal(err)
	}
	counter := uint32(challenge[7]) | uint32(challenge[8])<<8 | uint32(challenge[9])<<16 | uint32(challenge[10])<<24
	rk := newSpeckKey(key[:])
	pt := make([]byte, 18)
	copy(pt, hostRandom)
	pt[7] = ^command
	copy(pt[8:11], g.Provisioning[7:10])
	copy(pt[11:18], challenge[:7])
	cryptCTR(&rk, authNonce(g.Provisioning.salt(), counter, TransportBLE, 0), pt)
	req = append([]byte{command}, pt...)
	return req, func(resp []byte) ([speckKeySize]byte, error) {
		p := append([]byte(nil), resp[:23]...)
		cryptCTR(&rk, authNonce(g.Provisioning.salt(), counter, TransportBLE, 1), p)
		if !bytes.Equal(p[:7], hostRandom) {
			return [speckKeySize]byte{}, errors.New("host random echo")
		}
		return deriveSessionKey(g.RootSlot, p[7:23])
	}
}

func TestGen2Login(t *testing.T) {
	st, err := NewState(15, time.Hour, 3*time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	prov := Gen2Provisioning{1, 2, 3, 4, 5, 6, 7, 8, 9, 10}
	for _, slot := range []int{1, 2} {
		g, err := NewGen2Sensor(st.Gen2UID(), slot, prov, nil)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := g.Frame(1, st.Gen2PacketPlain(180)); err == nil {
			t.Fatal("frame before login")
		}
		ch := g.Challenge()
		if len(ch) != Gen2ChallengeSize {
			t.Fatalf("challenge %d bytes", len(ch))
		}
		hr := []byte{1, 2, 3, 4, 5, 6, 7}
		req, finish := gen2Client(t, g, 0x1f, hr, ch)
		resp, err := g.Accept(req)
		if err != nil {
			t.Fatalf("slot %d: %v", slot, err)
		}
		sk, err := finish(resp)
		if err != nil {
			t.Fatalf("slot %d: %v", slot, err)
		}
		plain := st.Gen2PacketPlain(180)
		f, err := g.Frame(0x0201, plain)
		if err != nil {
			t.Fatal(err)
		}
		got, err := decryptBLEFrame(sk, prov.streamPrefix(), f)
		if err != nil {
			t.Fatalf("slot %d: %v", slot, err)
		}
		if f[0] != 0x01 || f[1] != 0x02 || !bytes.Equal(got[2:], plain) {
			t.Fatalf("slot %d: frame round trip", slot)
		}

		// A replayed request (challenge consumed) and a tampered one fail.
		if _, err := g.Accept(req); !errors.Is(err, ErrGen2Auth) {
			t.Fatal("replay accepted")
		}
		ch = g.Challenge()
		req, _ = gen2Client(t, g, 0x1f, hr, ch)
		req[12] ^= 1 // inside the sensor-random echo (the host random is only echoed back)
		if _, err := g.Accept(req); !errors.Is(err, ErrGen2Auth) {
			t.Fatal("tampered request accepted")
		}
	}
}
