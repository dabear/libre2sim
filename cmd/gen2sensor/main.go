// SPDX-License-Identifier: GPL-3.0-or-later
//
// gen2sensor plays a FreeStyle Libre 2 Gen2 sensor (libre2sim's Sensor in Gen2
// mode) over TCP, so a Gen2 client (Juggluco's protocol.c via
// tools/gen2bridge, or another implementation) can be checked against it
// without a real sensor. Each message goes whole, in order.
//
// With -nfc the client first runs the NFC side: it sends 'N', then NFC
// custom commands 0xA1 as [len][parameters], each answered [status][len]
// [reply] (status 0, or the NFC error code with no reply), the reply as
// CoreNFC returns it; len 0 ends the NFC phase. Without -nfc NFC is mocked:
// streaming is enabled as POST /gen2/test-pairing does.
//
// Then BLE: client → 0x20; sensor → 14 challenge; client → 19 auth request;
// sensor → 25 auth response; sensor → 46-byte frames.
//
// It prints JSON lines: first the sensor's identity (addr, uid, slot,
// provisioning, bleAddress); with -nfc, after the NFC phase, the plain FRAM
// and whether NFC enabled streaming; then each frame's 44-byte plaintext and
// the notify sizes it goes out as over BLE.
package main

import (
	"bufio"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log"
	"net"
	"os"
	"time"

	"libre2sim/libre2"
)

func main() {
	addr := flag.String("addr", "127.0.0.1:0", "listen address")
	slot := flag.Int("slot", 1, "root slot (1 or 2)")
	frames := flag.Int("frames", 5, "frames to send")
	age := flag.Duration("age", 3*time.Hour, "sensor age")
	nfc := flag.Bool("nfc", false, "the client runs the Gen2 NFC flow first")
	flag.Parse()

	state, err := libre2.NewState(15, time.Hour, *age)
	if err != nil {
		log.Fatal(err)
	}
	state.SetGen2(true)
	state.Gen2RootSlot = *slot
	dir, err := os.MkdirTemp("", "gen2sensor")
	if err != nil {
		log.Fatal(err)
	}
	defer os.RemoveAll(dir)
	sensor := libre2.NewSensor(state, dir+"/state.json")
	if !*nfc {
		if _, err := sensor.Gen2TestPairing(time.Now()); err != nil {
			log.Fatal(err)
		}
	}

	ln, err := net.Listen("tcp", *addr)
	if err != nil {
		log.Fatal(err)
	}
	stream, _ := hex.DecodeString(state.Gen2Stream)
	out := json.NewEncoder(os.Stdout)
	out.Encode(map[string]any{
		"addr":         ln.Addr().String(),
		"uid":          state.UID,
		"provisioning": hex.EncodeToString(stream[:libre2.Gen2ProvisioningSize]),
		"slot":         *slot,
		"bleAddress":   state.BLEAddress,
	})
	c, err := ln.Accept()
	if err != nil {
		log.Fatal(err)
	}
	defer c.Close()
	if err := serve(c, sensor, state, *frames, *nfc, out); err != nil {
		log.Printf("connection: %v", err)
		os.Exit(1)
	}
}

func serve(c net.Conn, s *libre2.Sensor, st *libre2.State, frames int, nfc bool, out *json.Encoder) error {
	r := bufio.NewReader(c)
	first, err := r.ReadByte()
	if err != nil {
		return err
	}
	if first == 'N' {
		if !nfc {
			return fmt.Errorf("client wants NFC; run with -nfc")
		}
		if err := serveNFC(r, c, s); err != nil {
			return err
		}
		enabled := s.Status()["streamingEnabled"] == true
		tp, err := s.Gen2TestPairing(time.Now()) // only to report the FRAM; NFC already enabled streaming or not
		if err != nil {
			return err
		}
		out.Encode(map[string]any{"nfc": "done", "streamingEnabledByNFC": enabled, "fram": tp.FRAM})
		if first, err = r.ReadByte(); err != nil {
			return err
		}
	}
	if first != 0x20 {
		return fmt.Errorf("want kickoff 0x20, got %#x", first)
	}
	ch, err := s.Gen2LoginChallenge(time.Now())
	if err != nil {
		return err
	}
	if _, err := c.Write(ch); err != nil {
		return err
	}
	req := make([]byte, libre2.Gen2AuthRequestSize)
	if _, err := io.ReadFull(r, req); err != nil {
		return err
	}
	resp, err := s.Gen2LoginAccept(req, time.Now())
	if err != nil {
		out.Encode(map[string]any{"auth": "rejected", "error": err.Error()})
		return err
	}
	if _, err := c.Write(resp); err != nil {
		return err
	}
	out.Encode(map[string]any{"auth": "ok"})
	lc := st.LifeCount(time.Now())
	for i := 0; i < frames; i++ {
		plain := st.Gen2PacketPlain(lc + i)
		chunks, err := s.Gen2FrameNotifies(lc + i)
		if err != nil {
			return err
		}
		var frame []byte
		var sizes []int
		for _, ch := range chunks {
			frame = append(frame, ch...)
			sizes = append(sizes, len(ch))
		}
		if _, err := c.Write(frame); err != nil {
			return err
		}
		out.Encode(map[string]any{"frame": i + 1, "plain": hex.EncodeToString(plain), "notifies": sizes})
	}
	return nil
}

func serveNFC(r *bufio.Reader, w io.Writer, s *libre2.Sensor) error {
	for {
		n, err := r.ReadByte()
		if err != nil {
			return err
		}
		if n == 0 {
			return nil
		}
		params := make([]byte, n)
		if _, err := io.ReadFull(r, params); err != nil {
			return err
		}
		reply, err := s.NFCCustom(0xa1, params, time.Now())
		status := byte(0)
		if err != nil {
			status = 0xff
			if e, ok := err.(*libre2.NFCError); ok {
				status = e.Code
			}
			log.Printf("NFC a1 %x: %v", params, err)
			reply = nil
		}
		if _, err := w.Write(append([]byte{status, byte(len(reply))}, reply...)); err != nil {
			return err
		}
	}
}
