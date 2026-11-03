package libre2

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"log"
	"time"
)

// NFC error codes. 0x01, 0x02 and 0x10 are ISO 15693's own; 0xA0…0xDF are
// left to the manufacturer.
const (
	nfcErrNotSupported = 0x01 // command not supported
	nfcErrFormat       = 0x02 // command not recognized, e.g. bad parameters
	nfcErrBlock        = 0x10 // block not available
	// nfcErrState refuses enable-streaming on a sensor that is not running
	// (sim's choice: a custom-range code; what a real sensor answers is
	// unknown, but it cannot hand out a MAC for a sensor that will never
	// stream).
	nfcErrState = 0xb0
)

// cmdA1 is the Libre 2's custom command: with no parameters it returns the
// patch info, with [0x1E, …] it enables streaming.
const cmdA1 = 0xa1

// NFCError is a sensor's error reply to an NFC command.
type NFCError struct {
	Code    byte
	Message string
}

func (e *NFCError) Error() string { return fmt.Sprintf("NFC error 0x%02x: %s", e.Code, e.Message) }

// NFCIdentifier is the tag identifier CoreNFC reports: the UID in tag order,
// the reverse of sensorUID.
func (s *Sensor) NFCIdentifier() []byte {
	s.mu.Lock()
	defer s.mu.Unlock()
	uid := s.state.uid()
	for i, j := 0, len(uid)-1; i < j; i, j = i+1, j-1 {
		uid[i], uid[j] = uid[j], uid[i]
	}
	return uid
}

// NFCReadBlocks is readMultipleBlocks: count 8-byte blocks of the encrypted
// FRAM from block first. Blocks 0…42 exist.
func (s *Sensor) NFCReadBlocks(first, count int) ([][]byte, error) {
	return s.readBlocks(first, count, time.Now())
}

// framSnapshot is how long one scan keeps reading the same FRAM image.
const framSnapshot = 30 * time.Second

func (s *Sensor) readBlocks(first, count int, now time.Time) ([][]byte, error) {
	if first < 0 || count < 1 || first+count > framBlocks {
		return nil, &NFCError{nfcErrBlock, fmt.Sprintf("blocks %d+%d: the sensor has blocks 0 to %d", first, count, framBlocks-1)}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	// A scan reads the FRAM in 15 requests. The sim builds the image once, at
	// block 0, and serves the rest of the scan from it (sim's choice), so a
	// minute ticking over mid-scan cannot leave the app with blocks from two
	// images and a body CRC that fails.
	if first == 0 || s.fram == nil || now.Sub(s.framAt) > framSnapshot || now.Before(s.framAt) {
		s.fram = s.state.encryptedFRAM(now)
		s.framAt = now
	}
	blocks := make([][]byte, count)
	for i := range blocks {
		b := first + i
		blocks[i] = append([]byte(nil), s.fram[b*8:b*8+8]...)
	}
	return blocks, nil
}

// NFCCustom answers an ISO 15693 custom command (manufacturer code 0x07) the
// way CoreNFC's customCommand returns it to LibreTransmitter:
//
//   - 0xA1 with no parameters: the 6-byte patch info.
//   - 0xA1 with [0x1E, code LE4, d4], enable streaming: d must be
//     usefulFunction(uid, 0x1E, infoWord ^ code[0…1]), as
//     SensorPairingService.nfcCommand computes it, or the sensor answers
//     0x02. A sensor that is not running (not activated, worn out or
//     terminated) answers 0xB0. Otherwise it remembers the code, which BLE
//     unlocks build on, and returns its BLE address least significant byte
//     first; the app reverses it into AdvertisedName.
//   - Anything else: 0x01.
func (s *Sensor) NFCCustom(code byte, params []byte, now time.Time) ([]byte, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	st := s.state
	switch {
	case code == cmdA1 && len(params) == 0:
		return st.patchInfo(), nil

	case code == cmdA1 && len(params) == 9 && params[0] == subcmdEnableStreaming:
		unlock := binary.LittleEndian.Uint32(params[1:5])
		want := enableStreamingD(st.uid(), st.patchInfo(), unlock)
		if !bytes.Equal(params[5:9], want[:]) {
			return nil, &NFCError{nfcErrFormat, fmt.Sprintf("enable streaming: d is %x, want %x for code %d", params[5:9], want, unlock)}
		}
		switch {
		case st.Activated == nil:
			return nil, &NFCError{nfcErrState, "enable streaming: the sensor is not activated"}
		case st.Terminated != nil:
			return nil, &NFCError{nfcErrState, "enable streaming: the sensor was terminated"}
		case st.lifeCount(now) >= st.WearMinutes:
			return nil, &NFCError{nfcErrState, "enable streaming: the sensor has expired"}
		}
		st.StreamingCode = &unlock
		// Unlocks count up from the new code, so the count starts over
		// (sim's choice).
		st.UnlockCount = 0
		s.save()
		log.Printf("NFC: streaming enabled with code %d; BLE name %s", unlock, st.AdvertisedName())
		s.events.Add(CatNFC, "streaming-enabled", "streaming enabled with code %d; BLE name %s", unlock, st.AdvertisedName())
		return st.macLE(), nil

	default:
		return nil, &NFCError{nfcErrNotSupported, fmt.Sprintf("unsupported command 0x%02x with parameters %x", code, params)}
	}
}

// EnableStreamingCommand is Libre2NFC.enableStreamingCommand
// (SensorPairingService.nfcCommand(.enableStreaming)): the custom command
// code and parameters an app sends to enable streaming with unlockCode.
// sensorUID is in sensorUID order. For clients and tests.
func EnableStreamingCommand(sensorUID, patchInfo []byte, unlockCode uint32) (code byte, params []byte) {
	return cmdA1, enableStreamingParams(sensorUID, patchInfo, unlockCode)
}
