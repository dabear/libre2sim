// SPDX-License-Identifier: GPL-3.0-or-later
//
// The sensor side of the FreeStyle Libre 2 "Gen2" BLE login, built on
// gen2_crypto.go (a port of Juggluco's GPL-3.0 protocol.c). It is the mirror
// image of Juggluco's client: where protocol.c builds the auth request and
// accepts the response, this checks the request and builds the response.
//
// The exchange, in BLE notification/write sizes:
//
//	host   → 0x20 (asks for a challenge)          [sim's socket framing]
//	sensor → 14-byte challenge: sensorRandom(7) ‖ counter LE(4) ‖ 3 bytes
//	host   → 19-byte auth request: command ‖ CTR(sensorKey, step 0,
//	         hostRandom(7) ‖ ^command ‖ provisioning[7:10] ‖ sensorRandom(7))
//	sensor → 25-byte response: CTR(sensorKey, step 1,
//	         hostRandom(7) ‖ sessionContext(16)) ‖ CRC(2)
//	sensor → 46-byte frames: seq(2) ‖ CTR(sessionKey, Gen1 44-byte plaintext)
//
// Juggluco reads the response as 7 + 18 bytes and the frame as 20 + 18 + 8
// bytes of notifications; the socket carries them whole.
//
// Not settled by the reference and chosen here (marked "sim's choice"):
// challenge[11:14] (Juggluco ignores them; sim sends zeros) and the response's
// two trailing bytes (unchecked on BLE; sim sends the CRC of the 23 encrypted
// bytes, as the NFC response carries).

package libre2

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"time"
)

// Gen2 BLE message sizes (protocol.h).
const (
	Gen2ChallengeSize    = 14
	Gen2AuthRequestSize  = 19
	Gen2AuthResponseSize = 25
	Gen2ProvisioningSize = 10
	Gen2FrameSize        = bleFrameSize
	gen2RandomSize       = 7
	gen2ContextSize      = 16
)

// Gen2Provisioning is what the phone saves over NFC when it enables
// streaming: streamPrefix(4) ‖ salt(3) ‖ 3 bytes echoed in the auth request.
type Gen2Provisioning [Gen2ProvisioningSize]byte

func (p Gen2Provisioning) streamPrefix() (b [4]byte) { copy(b[:], p[0:4]); return }
func (p Gen2Provisioning) salt() (b [3]byte)         { copy(b[:], p[4:7]); return }

// Gen2Sensor is one sensor's Gen2 BLE login state. It is not safe for
// concurrent use.
type Gen2Sensor struct {
	UID          []byte // 8 bytes, as read over NFC
	RootSlot     int    // 1 or 2
	Provisioning Gen2Provisioning
	// Epoch is the NFC epoch the NFC challenge carries and the NFC data nonce
	// uses (protocol.c's nfc_epoch). Not used over BLE.
	Epoch [2]byte

	sensorKey    [speckKeySize]byte
	counter      uint32
	sensorRandom [gen2RandomSize]byte
	challenged   bool
	sessionKey   *[speckKeySize]byte
	random       func(n int) []byte

	// The NFC exchange (gen2_nfc.go): its own outstanding challenge, and the
	// data key an authenticated FRAM read (0x1F) established.
	nfcRandom     [gen2RandomSize]byte
	nfcChallenged bool
	dataKey       *[speckKeySize]byte
}

// NewGen2Sensor derives the sensor key. random supplies the sensor random and
// session context; nil uses crypto/rand.
func NewGen2Sensor(uid []byte, rootSlot int, prov Gen2Provisioning, random func(int) []byte) (*Gen2Sensor, error) {
	if len(uid) != 8 {
		return nil, fmt.Errorf("libre2: Gen2 UID is %d bytes, want 8", len(uid))
	}
	key, err := deriveSensorKey(rootSlot, uid)
	if err != nil {
		return nil, err
	}
	if random == nil {
		random = func(n int) []byte { return randomFn(n) }
	}
	return &Gen2Sensor{UID: append([]byte(nil), uid...), RootSlot: rootSlot,
		Provisioning: prov, sensorKey: key, random: random}, nil
}

// Challenge starts a login: a fresh sensor random and the next counter.
func (g *Gen2Sensor) Challenge() []byte {
	g.counter++
	copy(g.sensorRandom[:], g.random(gen2RandomSize))
	g.challenged = true
	g.sessionKey = nil
	c := make([]byte, Gen2ChallengeSize)
	copy(c, g.sensorRandom[:])
	binary.LittleEndian.PutUint32(c[7:], g.counter)
	return c // c[11:14] zero: sim's choice
}

// ErrGen2Auth is returned for a request that does not authenticate.
var ErrGen2Auth = errors.New("libre2: Gen2 auth request rejected")

// Accept checks a 19-byte auth request against the last challenge and, if it
// authenticates, returns the 25-byte response and keeps the session key.
func (g *Gen2Sensor) Accept(req []byte) ([]byte, error) {
	if !g.challenged {
		return nil, fmt.Errorf("%w: no challenge outstanding", ErrGen2Auth)
	}
	g.challenged = false
	if len(req) != Gen2AuthRequestSize {
		return nil, fmt.Errorf("%w: %d bytes, want %d", ErrGen2Auth, len(req), Gen2AuthRequestSize)
	}
	rk := newSpeckKey(g.sensorKey[:])
	command := req[0]
	pt := append([]byte(nil), req[1:]...)
	cryptCTR(&rk, authNonce(g.Provisioning.salt(), g.counter, TransportBLE, 0), pt)
	switch {
	case pt[7] != ^command:
		return nil, fmt.Errorf("%w: command check", ErrGen2Auth)
	case !bytes.Equal(pt[8:11], g.Provisioning[7:10]):
		return nil, fmt.Errorf("%w: provisioning echo", ErrGen2Auth)
	case !bytes.Equal(pt[11:18], g.sensorRandom[:]):
		return nil, fmt.Errorf("%w: sensor random echo", ErrGen2Auth)
	}
	hostRandom := pt[0:7]

	context := g.random(gen2ContextSize)
	sk, err := deriveSessionKey(g.RootSlot, context)
	if err != nil {
		return nil, err
	}
	resp := make([]byte, Gen2AuthResponseSize)
	copy(resp, hostRandom)
	copy(resp[7:23], context)
	cryptCTR(&rk, authNonce(g.Provisioning.salt(), g.counter, TransportBLE, 1), resp[:23])
	crc := libreCRC16(resp[:23])
	resp[23], resp[24] = byte(crc>>8), byte(crc) // sim's choice; unchecked on BLE
	g.sessionKey = &sk
	return resp, nil
}

// Frame encrypts a Gen1-layout 44-byte plaintext (records ‖ age ‖ CRC, as
// packetPlain builds it) into a 46-byte Gen2 frame under the session key.
func (g *Gen2Sensor) Frame(seq uint16, plain44 []byte) ([]byte, error) {
	if g.sessionKey == nil {
		return nil, errors.New("libre2: Gen2 frame before login")
	}
	if len(plain44) != packetPlainSz || !packetCRCValid(plain44) {
		return nil, errors.New("libre2: Gen2 frame plaintext must be 44 bytes with a valid CRC")
	}
	return encryptBLEFrame(*g.sessionKey, g.Provisioning.streamPrefix(), byte(seq), byte(seq>>8), plain44[:bleFrameDataSz])
}

// frameAsIs encrypts plain44 without checking or rewriting its CRC, for the
// badcrc fault.
func (g *Gen2Sensor) frameAsIs(seq uint16, plain44 []byte) []byte {
	frame := make([]byte, bleFrameSize)
	frame[0], frame[1] = byte(seq), byte(seq>>8)
	copy(frame[2:], plain44)
	rk := newSpeckKey(g.sessionKey[:])
	cryptCTR(&rk, bleFrameNonce(g.Provisioning.streamPrefix(), frame[0], frame[1]), frame[2:])
	return frame
}

// LoggedIn is whether a BLE login completed since the last challenge.
func (g *Gen2Sensor) LoggedIn() bool { return g.sessionKey != nil }

// Gen2PacketPlain is the 44-byte Gen1-layout plaintext for life count lc,
// for feeding Frame.
func (s *State) Gen2PacketPlain(lc int) []byte { return s.packetPlain(lc) }

// Gen2UID is the state's 8-byte UID.
func (s *State) Gen2UID() []byte { return s.uid() }

// randomFn is the default source of sensor randoms and session contexts.
var randomFn = random

// LifeCount is the sensor's age in minutes at now (0 before activation).
func (s *State) LifeCount(now time.Time) int { return s.lifeCount(now) }
