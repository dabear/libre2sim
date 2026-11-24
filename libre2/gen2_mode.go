// SPDX-License-Identifier: GPL-3.0-or-later
//
// Gen2 mode: the same sensor, but with a "generation 2" security on both NFC
// and BLE (gen2_nfc.go, gen2.go). It is switched at run time (POST /config
// {"gen2":true}, the control panel's Settings toggle) and saved in the state
// file. With it off, nothing here runs and the sensor is the Gen1 sensor it
// always was.
//
// Switching generation is treated as swapping the sensor's security (sim's
// choice): streaming is disabled (an app must enable it again over NFC, with
// the new generation's commands) and the caller drops any BLE link.

package libre2

import (
	"encoding/hex"
	"fmt"
	"log"
	"time"
)

// gen2PatchInfo2 is patchInfo[2] of a Gen2 sensor: product type 3 (Libre 2)
// in the high nibble, security version 9 in the low one. Gen1 is 0x31.
// LibreTransmitter's Libre2Gen2.securityGeneration and Juggluco's
// getinfogen both read generation 2 from a security version of 9 or more.
const gen2PatchInfo2 = 0x39

// gen2StreamSize is what State.Gen2Stream holds: streamPrefix(4) ‖ salt(3) ‖
// the 3 bytes that end the BLE provisioning ‖ NFC epoch(2).
const gen2StreamSize = 12

// gen2DefaultRootSlot is the root slot a sim sensor reports over A1 22 (sim's
// choice; Juggluco knows slots 1 and 2).
const gen2DefaultRootSlot = 1

// effectivePatchInfo is the patch info the sensor reports: the stored one,
// with the Gen2 security version while Gen2 mode is on.
func (s *State) effectivePatchInfo() []byte {
	p := hexBytes(s.PatchInfo)
	if s.Gen2 && len(p) > 2 {
		p[2] = gen2PatchInfo2
	}
	return p
}

// streamingEnabled is whether an app enabled streaming over NFC, in the
// sensor's current generation.
func (s *State) streamingEnabled() bool {
	if s.Gen2 {
		return s.Gen2StreamingEnabled
	}
	return s.StreamingCode != nil
}

// Gen2 is whether Gen2 mode is on.
func (s *Sensor) Gen2() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.state.Gen2
}

// SetGen2 switches Gen2 mode. It reports whether anything changed; on a
// change streaming is disabled in both generations and the caller should
// drop the BLE link.
func (s *Sensor) SetGen2(on bool) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	st := s.state
	if st.Gen2 == on {
		return false
	}
	st.SetGen2(on)
	s.gen2 = nil
	s.gen2FRAM = nil
	s.changed()
	gen := map[bool]string{false: "Gen1", true: "Gen2"}[on]
	log.Printf("sensor is now %s (patch info %x); streaming must be enabled again over NFC", gen, st.effectivePatchInfo())
	s.events.Add(CatSensor, "generation", "sensor is now %s (patch info %x); streaming disabled until enabled again over NFC",
		gen, st.effectivePatchInfo())
	return true
}

// SetGen2 switches a state's generation, disabling streaming. For a sensor
// that is running, use Sensor.SetGen2.
func (st *State) SetGen2(on bool) {
	st.Gen2 = on
	st.StreamingCode = nil
	st.UnlockCount = 0
	st.Gen2StreamingEnabled = false
	if on {
		if st.Gen2RootSlot == 0 {
			st.Gen2RootSlot = gen2DefaultRootSlot
		}
		if len(hexBytes(st.Gen2Stream)) != gen2StreamSize {
			// The stream prefix, salt, provisioning tail and NFC epoch are
			// fixed per sensor (sim's choice: a real sensor's lifetime of
			// them is unknown).
			st.Gen2Stream = hex.EncodeToString(random(gen2StreamSize))
		}
	}
}

// gen2Sensor is the Gen2 protocol state for the current sensor, with mu held.
func (s *Sensor) gen2Sensor() (*Gen2Sensor, error) {
	if s.gen2 != nil {
		return s.gen2, nil
	}
	st := s.state
	stream := hexBytes(st.Gen2Stream)
	if len(stream) != gen2StreamSize {
		return nil, fmt.Errorf("gen2 stream parameters missing from the state")
	}
	var prov Gen2Provisioning
	copy(prov[:], stream[:Gen2ProvisioningSize])
	g, err := NewGen2Sensor(st.uid(), st.Gen2RootSlot, prov, nil)
	if err != nil {
		return nil, err
	}
	copy(g.Epoch[:], stream[Gen2ProvisioningSize:])
	s.gen2 = g
	return g, nil
}

// nfcGen2 answers a Gen2 sensor's 0xA1 subcommands (see gen2_nfc.go), with mu
// held. params is non-empty.
func (s *Sensor) nfcGen2(params []byte, now time.Time) ([]byte, error) {
	st := s.state
	g, err := s.gen2Sensor()
	if err != nil {
		return nil, &NFCError{nfcErrState, err.Error()}
	}
	sub := params[0]
	switch {
	case sub == gen2NFCReadRootSlot && len(params) == 1:
		return g.NFCRootSlotReply(), nil

	case sub == gen2NFCReadChallenge && len(params) == 1:
		return g.NFCChallenge(), nil

	case sub == gen2NFCReadBlocks && len(params) == 3:
		first, count := int(params[1]), int(params[2])+1
		// One scan reads the same FRAM image, as Gen1's readBlocks does.
		if first == 0 || s.gen2FRAM == nil || now.Sub(s.framAt) > framSnapshot || now.Before(s.framAt) {
			s.gen2FRAM = st.plainFRAM(now)
			s.framAt = now
		}
		out, err := g.nfcReadBlocks(s.gen2FRAM, first, count)
		if err != nil {
			return nil, &NFCError{nfcErrBlock, err.Error()}
		}
		return out, nil

	case len(params) == gen2NFCAuthParamsSize:
		cmd, hostRandom, err := g.nfcAuthenticate(params)
		if err != nil {
			s.events.Add(CatNFC, "gen2-auth-refused", "%v", err)
			return nil, &NFCError{nfcErrFormat, err.Error()}
		}
		switch cmd {
		case gen2NFCAuthFRAMRead:
			return g.nfcAuthFRAMRead(hostRandom)
		case gen2NFCEnableStreaming:
			switch {
			case st.Activated == nil:
				return nil, &NFCError{nfcErrState, "enable streaming: the sensor is not activated"}
			case st.Terminated != nil:
				return nil, &NFCError{nfcErrState, "enable streaming: the sensor was terminated"}
			case st.lifeCount(now) >= st.WearMinutes:
				return nil, &NFCError{nfcErrState, "enable streaming: the sensor has expired"}
			}
			payload := append(st.macLE(), g.Provisioning[7:10]...)
			st.Gen2StreamingEnabled = true
			s.save()
			log.Printf("NFC: Gen2 streaming enabled; BLE name %s", st.AdvertisedName())
			s.events.Add(CatNFC, "streaming-enabled", "Gen2 streaming enabled (authenticated 0x1E); BLE name %s", st.AdvertisedName())
			return g.nfcCommandReply(hostRandom, payload), nil
		default:
			// 0x1B (activate) and anything else: not emulated (sim's
			// choice; POST /activate starts the sensor instead).
			return nil, &NFCError{nfcErrNotSupported, fmt.Sprintf("authenticated command 0x%02x is not emulated", cmd)}
		}

	case sub == subcmdEnableStreaming:
		// The Gen1 enable-streaming request. A Gen2 sensor needs the
		// authenticated one (sim's choice of answer: 0x02).
		return nil, &NFCError{nfcErrFormat, "enable streaming: a Gen2 sensor needs the authenticated 0x1E (A1 20 challenge first)"}
	}
	return nil, &NFCError{nfcErrNotSupported, fmt.Sprintf("unsupported Gen2 command 0xa1 with parameters %x", params)}
}

// gen2Challenge starts a BLE login: the 14-byte challenge for F001.
func (s *Sensor) gen2Challenge(now time.Time) ([]byte, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.gen2CanStream(now); err != nil {
		return nil, err
	}
	g, err := s.gen2Sensor()
	if err != nil {
		return nil, err
	}
	return g.Challenge(), nil
}

// gen2Accept checks the 19-byte login write and returns the 25-byte response.
func (s *Sensor) gen2Accept(req []byte, now time.Time) ([]byte, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.gen2CanStream(now); err != nil {
		return nil, err
	}
	g, err := s.gen2Sensor()
	if err != nil {
		return nil, err
	}
	return g.Accept(req)
}

func (s *Sensor) gen2CanStream(now time.Time) error {
	switch {
	case !s.state.Gen2StreamingEnabled:
		return fmt.Errorf("streaming was never enabled over NFC")
	case !s.state.running(now):
		return fmt.Errorf("the sensor has ended")
	}
	return nil
}

// gen2Notifies is notifies for a Gen2 sensor, with mu held: the frame as
// 20 + 18 + 8 byte notifies (LibreTransmitter's Libre2DirectTransmitter and
// Juggluco's Libre2GattCallback both reassemble it so).
func (s *Sensor) gen2Notifies(lc int, fault string) ([][]byte, error) {
	g, err := s.gen2Sensor()
	if err != nil || !g.LoggedIn() {
		return nil, fmt.Errorf("gen2 frame before login")
	}
	s.nonce++
	plain := s.state.packetPlain(lc)
	if fault == FaultBadCRC {
		plain[bleCRCOffset] ^= 0xff
		plain[bleCRCOffset+1] ^= 0xff
	}
	frame := g.frameAsIs(s.nonce, plain)
	if fault == FaultShort {
		frame = frame[:shortPacketSize]
	}
	return gen2Chunks(frame), nil
}

// gen2Chunks splits a frame into 20, 18 and the rest (8 for a whole frame).
func gen2Chunks(frame []byte) [][]byte {
	var out [][]byte
	for _, n := range []int{20, 18} {
		if len(frame) <= n {
			break
		}
		out = append(out, frame[:n])
		frame = frame[n:]
	}
	return append(out, frame)
}
