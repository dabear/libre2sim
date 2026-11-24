// SPDX-License-Identifier: GPL-3.0-or-later
//
// The sensor side of the Gen2 NFC exchange, mirroring Juggluco's client
// (Gen2.java prepareNfcCommand / readAuthenticatedFram / enableStreaming over
// protocol.c's l2g2_build_nfc_auth_request, l2g2_accept_auth_response,
// l2g2_read_command_reply and l2g2_decrypt_nfc_blocks). All commands are
// ISO 15693 custom command 0xA1 (manufacturer 07); the byte layouts below are
// what CoreNFC's customCommand returns, i.e. without the response-flags byte
// Juggluco's raw frames start with.
//
//	A1 22          read root slot -> x x slot LE2                     (Juggluco: raw[3..4])
//	A1 20          read challenge -> 25 bytes: sensorRandom(7) ‖ counter LE4 ‖
//	                                 3 bytes ‖ salt(3) ‖ streamPrefix(4) ‖ epoch(2) ‖ CRC(2)
//	A1 <cmd> ‖ 15  authenticated command: CTR(sensorKey, step 0,
//	                                 hostRandom(7) ‖ ^cmd ‖ sensorRandom(7))
//	   cmd 1F      authenticate FRAM read -> header(8) ‖ CTR(step 1, hostRandom ‖ context16) ‖ CRC
//	               (the data key is derived from context16 with the session root)
//	   cmd 1E      enable streaming -> header(8) ‖ CTR(step 1, sensorRandom ‖ payload(9) ‖ hostRandom) ‖ CRC
//	               payload = BLE address(6) ‖ the 3 bytes that end the BLE provisioning
//	A1 21 b n-1    read n blocks from b -> header(8) ‖ n×8 bytes, each block XORed with
//	               Speck(dataKey, 00 ‖ b ‖ streamPrefix (top bit of the last byte clear) ‖ epoch)
//
// The CRCs are placed as protocol.c's matches_crc reads them, which is the
// same placement as a Gen1 packet's (libreCRC16, high byte first).
//
// Sim's choices, not settled by the reference: challenge bytes 11…13 and the
// 8-byte reply headers are zeros; the root-slot reply's first two bytes are
// zeros; the BLE address in the 0x1E payload is least significant byte first,
// as Gen1's enable-streaming reply is; one counter serves NFC and BLE.

package libre2

import (
	"bytes"
	"encoding/binary"
	"fmt"
)

// Gen2 NFC subcommands of 0xA1 (Juggluco Gen2.java).
const (
	gen2NFCActivate        = 0x1b
	gen2NFCEnableStreaming = 0x1e
	gen2NFCAuthFRAMRead    = 0x1f
	gen2NFCReadChallenge   = 0x20
	gen2NFCReadBlocks      = 0x21
	gen2NFCReadRootSlot    = 0x22

	gen2NFCChallengeSize  = 25
	gen2NFCAuthParamsSize = 16 // cmd ‖ 15 encrypted bytes
	gen2NFCReplyHeader    = 8
	gen2NFCMaxReadBlocks  = 3
)

// NFCRootSlotReply answers A1 22.
func (g *Gen2Sensor) NFCRootSlotReply() []byte {
	return []byte{0, 0, byte(g.RootSlot), byte(g.RootSlot >> 8)}
}

// NFCChallenge answers A1 20: it starts an NFC exchange.
func (g *Gen2Sensor) NFCChallenge() []byte {
	g.counter++
	copy(g.nfcRandom[:], g.random(gen2RandomSize))
	g.nfcChallenged = true
	g.dataKey = nil
	c := make([]byte, gen2NFCChallengeSize)
	copy(c, g.nfcRandom[:])
	binary.LittleEndian.PutUint32(c[7:], g.counter)
	// c[11:14] zero: sim's choice
	salt, prefix := g.Provisioning.salt(), g.Provisioning.streamPrefix()
	copy(c[14:17], salt[:])
	copy(c[17:21], prefix[:])
	copy(c[21:23], g.Epoch[:])
	putCRC(c[:23], c[23:])
	return c
}

// gen2CRC is protocol.c's l2g2_crc16 in libreCRC16's byte order: the same
// CRC, except that an odd-length input is padded with a zero byte. The NFC
// challenge and replies CRC 23 bytes, so the padding matters there; every
// Gen1 region, and a BLE frame's 42 bytes, are even.
func gen2CRC(data []byte) uint16 {
	if len(data)%2 == 1 {
		data = append(append([]byte(nil), data...), 0)
	}
	return libreCRC16(data)
}

// gen2CRCValid is matches_crc: the CRC of b[:len-2] in its last two bytes.
func gen2CRCValid(b []byte) bool {
	n := len(b)
	return n >= 2 && gen2CRC(b[:n-2]) == word(b[n-2], b[n-1])
}

// putCRC writes the CRC of data into dst[0:2] as matches_crc reads it.
func putCRC(data, dst []byte) {
	crc := gen2CRC(data)
	dst[0], dst[1] = byte(crc>>8), byte(crc)
}

// NFCAuthenticated checks an authenticated command (cmd ‖ 15 bytes) against
// the outstanding NFC challenge and returns the command and the host random.
func (g *Gen2Sensor) nfcAuthenticate(params []byte) (byte, []byte, error) {
	if !g.nfcChallenged {
		return 0, nil, fmt.Errorf("%w: no NFC challenge outstanding", ErrGen2Auth)
	}
	g.nfcChallenged = false
	if len(params) != gen2NFCAuthParamsSize {
		return 0, nil, fmt.Errorf("%w: %d bytes, want %d", ErrGen2Auth, len(params), gen2NFCAuthParamsSize)
	}
	cmd := params[0]
	pt := append([]byte(nil), params[1:]...)
	rk := newSpeckKey(g.sensorKey[:])
	cryptCTR(&rk, authNonce(g.Provisioning.salt(), g.counter, TransportNFC, 0), pt)
	switch {
	case pt[7] != ^cmd:
		return 0, nil, fmt.Errorf("%w: command check", ErrGen2Auth)
	case !bytes.Equal(pt[8:15], g.nfcRandom[:]):
		return 0, nil, fmt.Errorf("%w: sensor random echo", ErrGen2Auth)
	}
	return cmd, pt[:7], nil
}

// nfcReply is header(8) ‖ CTR(sensorKey, step 1, body) ‖ CRC(encrypted body).
func (g *Gen2Sensor) nfcReply(body []byte) []byte {
	out := make([]byte, gen2NFCReplyHeader+len(body)+2)
	enc := out[gen2NFCReplyHeader : gen2NFCReplyHeader+len(body)]
	copy(enc, body)
	rk := newSpeckKey(g.sensorKey[:])
	cryptCTR(&rk, authNonce(g.Provisioning.salt(), g.counter, TransportNFC, 1), enc)
	putCRC(enc, out[gen2NFCReplyHeader+len(body):])
	return out
}

// NFCAuthFRAMRead answers an authenticated 0x1F: it establishes the data key.
func (g *Gen2Sensor) nfcAuthFRAMRead(hostRandom []byte) ([]byte, error) {
	context := g.random(gen2ContextSize)
	k, err := deriveSessionKey(g.RootSlot, context)
	if err != nil {
		return nil, err
	}
	g.dataKey = &k
	return g.nfcReply(append(append([]byte(nil), hostRandom...), context...)), nil
}

// nfcCommandReply answers an authenticated command with payload.
func (g *Gen2Sensor) nfcCommandReply(hostRandom, payload []byte) []byte {
	body := append(append(append([]byte(nil), g.nfcRandom[:]...), payload...), hostRandom...)
	return g.nfcReply(body)
}

// nfcReadBlocks answers A1 21: count blocks of fram from first, encrypted
// under the data key.
func (g *Gen2Sensor) nfcReadBlocks(fram []byte, first, count int) ([]byte, error) {
	if g.dataKey == nil {
		return nil, fmt.Errorf("%w: block read without an authenticated FRAM read (0x1F)", ErrGen2Auth)
	}
	if first < 0 || count < 1 || count > gen2NFCMaxReadBlocks || (first+count)*8 > len(fram) {
		return nil, fmt.Errorf("blocks %d+%d out of range", first, count)
	}
	out := make([]byte, gen2NFCReplyHeader+count*8)
	rk := newSpeckKey(g.dataKey[:])
	prefix := g.Provisioning.streamPrefix()
	for i := 0; i < count; i++ {
		b := first + i
		var nonce [speckBlockSize]byte
		nonce[1] = byte(b)
		copy(nonce[2:6], prefix[:])
		nonce[5] &= 0x7f
		copy(nonce[6:8], g.Epoch[:])
		ks := rk.encrypt(nonce[:])
		blk := out[gen2NFCReplyHeader+i*8 : gen2NFCReplyHeader+i*8+8]
		for j := range blk {
			blk[j] = fram[b*8+j] ^ ks[j]
		}
	}
	return out, nil
}
