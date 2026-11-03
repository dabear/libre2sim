// BLE packet encryption, ported from LoopKit/LibreTransmitter
// (Libre2.decryptBLE in LibreSensor/SensorContents/PreLibre2.swift @ 4110512),
// MIT License, Copyright (c) 2016 Mark Wilson and contributors.

package libre2

import (
	"errors"
	"fmt"
	"log"
)

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

// Building the packets a streaming sensor notifies on F002, in the layout
// PreLibre2.parseBLEData (LoopKit/LibreTransmitter @ 4110512) reads.

// trendAges are how many minutes before the packet's age each of its seven
// trend records is: parseBLEData's ints.
var trendAges = [7]int{0, 2, 4, 6, 7, 12, 15}

const (
	bleRecordSize  = 4 // one trend or history record in a packet
	bleTrend       = 7 // records 0…6
	bleHistory     = 3 // records 7…9
	bleHistoryLag  = 2 // parseBLEData's delay
	bleAgeOffset   = 40
	bleCRCOffset   = 42
	bleNotifyChunk = 20 // the sensor's notify size: 46 bytes go as 20 + 20 + 6
)

// historyMinute is the minute parseBLEData stamps history record i (7…9) of
// a packet of age lc with: ((lc−2)/15)·15 − 15·(i−7). Swift's and Go's / both
// truncate toward zero, so this is the same for lc < 2.
func historyMinute(lc, i int) int {
	return ((lc-bleHistoryLag)/15)*15 - 15*(i-bleTrend)
}

// putBLERecord writes a 4-byte packet record in parseBLEData's layout: raw
// glucose in bits 0…13, raw temperature >> 2 in bits 14…25, |adjustment| >> 2
// in bits 26…30 and its sign in bit 31.
func putBLERecord(b []byte, raw uint16, rawTemperature, rawTemperatureAdjustment int) {
	for i := range b[:bleRecordSize] {
		b[i] = 0
	}
	writeBits(b, 0, 0, 0xe, int(raw))
	writeBits(b, 0, 0xe, 0xc, rawTemperature>>2)
	adj, neg := rawTemperatureAdjustment, 0
	if adj < 0 {
		adj, neg = -adj, 1
	}
	writeBits(b, 0, 0x1a, 0x5, adj>>2)
	writeBits(b, 0, 0x1f, 0x1, neg)
}

// packetPlain is the 44-byte plaintext of the packet for life count lc, with
// mu held: 7 trend records at lc − trendAges, 3 history records at
// historyMinute, age = lc LE at bytes 40–41, and the CRC of bytes 0…41
// big-endian at 42–43. Each record is its minute's State.record: the
// modelled glucose as raw glucose at that minute's tissue temperature, and
// the skin temperature's fields. A minute before the sensor started is an all-zero record; a minute
// without a reading (warmup) has raw 0. parseBLEData skips both.
func (s *State) packetPlain(lc int) []byte {
	pt := make([]byte, packetPlainSz)
	if s.Activated != nil {
		s.fill(lc)
	}
	record := func(i, minute int) {
		if s.Activated == nil || minute < 0 {
			return
		}
		raw, t, adj := s.record(minute)
		putBLERecord(pt[i*bleRecordSize:], raw, t, adj)
	}
	for i, ago := range trendAges {
		record(i, lc-ago)
	}
	for i := bleTrend; i < bleTrend+bleHistory; i++ {
		record(i, historyMinute(lc, i))
	}
	pt[bleAgeOffset], pt[bleAgeOffset+1] = byte(lc), byte(lc>>8)
	crc := libreCRC16(pt[:bleCRCOffset])
	pt[bleCRCOffset], pt[bleCRCOffset+1] = byte(crc>>8), byte(crc)
	return pt
}

// packetPlain is State.packetPlain under the sensor's lock.
func (s *Sensor) packetPlain(lc int) []byte {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.state.packetPlain(lc)
}

// packet is the encrypted 46-byte packet for life count lc. Its nonce bytes
// n0 n1 are a per-sensor counter, low byte first (sim's choice: the app
// takes whatever the sensor sends; a counter never repeats a keystream
// within 65536 packets). The counter lives only while the emulator runs.
func (s *Sensor) packet(lc int) []byte {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.packetLocked(lc, false)
}

func (s *Sensor) packetLocked(lc int, badCRC bool) []byte {
	s.nonce++
	pt := s.state.packetPlain(lc)
	if badCRC {
		// Invert the CRC, which always changes it, before encrypting.
		pt[bleCRCOffset] ^= 0xff
		pt[bleCRCOffset+1] ^= 0xff
	}
	return encryptPacket(s.state.uid(), byte(s.nonce), byte(s.nonce>>8), pt)
}

// Faults the sensor can be armed with (POST /fault). An armed fault spoils
// the next packet only and then disarms itself.
const (
	FaultNone   = "none"
	FaultBadCRC = "badcrc"
	FaultShort  = "short"
)

// shortPacketSize is how much of a packet the "short" fault sends: chunks of
// 20 + 20 + 5 bytes.
const shortPacketSize = packetSize - 1

// ArmFault arms a one-shot fault for the next packet, or with FaultNone
// disarms it.
func (s *Sensor) ArmFault(kind string) error {
	switch kind {
	case FaultNone, FaultBadCRC, FaultShort:
	default:
		return fmt.Errorf("unknown fault %q; want %q, %q or %q", kind, FaultBadCRC, FaultShort, FaultNone)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if kind == FaultNone {
		kind = ""
	}
	s.fault = kind
	if kind == "" {
		s.events.Add(CatSensor, "fault-disarmed", "fault disarmed")
	} else {
		s.events.Add(CatSensor, "fault-armed", "fault %s armed for the next packet", kind)
	}
	return nil
}

// Fault is the fault armed for the next packet, or "".
func (s *Sensor) Fault() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.fault
}

// notifies is what the sensor notifies on F002 for life count lc: the packet
// as 20 + 20 + 6 byte chunks, spoiled if a fault is armed (which it disarms).
//
//   - badcrc: the plaintext CRC is inverted before encryption, so
//     LibreTransmitter's decryptBLE fails with checksumValidationError.
//   - short: only the first 45 bytes go out, as 20 + 20 + 5. LibreTransmitter
//     parses its receive buffer only when it holds exactly 46 bytes, so the
//     buffer then holds 45 stale bytes until its next reset (a reconnect),
//     and the next minute's 46 bytes overshoot it and are never parsed. That
//     is the point of the fault: how the app copes with a truncated packet.
func (s *Sensor) notifies(lc int) [][]byte {
	s.mu.Lock()
	defer s.mu.Unlock()
	fault := s.fault
	s.fault = ""
	pkt := s.packetLocked(lc, fault == FaultBadCRC)
	if fault == FaultShort {
		pkt = pkt[:shortPacketSize]
	}
	if fault != "" {
		log.Printf("sending the packet for minute %d with fault %s", lc, fault)
		s.events.Add(CatSensor, "fault-fired", "fault %s spoiled the packet for minute %d", fault, lc)
	}
	return notifyChunks(pkt)
}

// notifyChunks splits a packet into the notifies the sensor sends it as:
// 20 + 20 + 6 bytes. LibreTransmitter appends them and parses the buffer when
// it holds exactly 46 bytes.
func notifyChunks(pkt []byte) [][]byte {
	var out [][]byte
	for len(pkt) > 0 {
		n := bleNotifyChunk
		if len(pkt) < n {
			n = len(pkt)
		}
		out = append(out, pkt[:n])
		pkt = pkt[n:]
	}
	return out
}
