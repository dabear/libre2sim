// SPDX-License-Identifier: GPL-3.0-or-later
//
// gen2sensor plays a FreeStyle Libre 2 Gen2 sensor over TCP, so a Gen2 client
// (Juggluco's protocol.c via tools/gen2bridge, or another implementation) can
// be checked against it without a real sensor. BLE is a byte pipe; the socket
// carries each BLE message whole, in order:
//
//	client → 0x20; sensor → 14 challenge; client → 19 auth request;
//	sensor → 25 auth response; sensor → 46-byte frames.
//
// NFC is mocked: on start it prints one JSON line with the UID, provisioning
// and root slot the client needs, and for each frame it sends a JSON line with
// the 44-byte plaintext it encrypted, so the client's output can be compared.
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
	frames := flag.Int("frames", 5, "frames to send per connection")
	age := flag.Duration("age", 3*time.Hour, "sensor age")
	provHex := flag.String("provisioning", "", "10-byte provisioning hex (default random)")
	once := flag.Bool("once", true, "exit after one connection")
	flag.Parse()

	state, err := libre2.NewState(15, time.Hour, *age)
	if err != nil {
		log.Fatal(err)
	}
	var prov libre2.Gen2Provisioning
	if *provHex != "" {
		b, err := hex.DecodeString(*provHex)
		if err != nil || len(b) != len(prov) {
			log.Fatalf("provisioning: want %d bytes of hex", len(prov))
		}
		copy(prov[:], b)
	} else if _, err := io.ReadFull(randReader{}, prov[:]); err != nil {
		log.Fatal(err)
	}
	sensor, err := libre2.NewGen2Sensor(state.Gen2UID(), *slot, prov, nil)
	if err != nil {
		log.Fatal(err)
	}

	ln, err := net.Listen("tcp", *addr)
	if err != nil {
		log.Fatal(err)
	}
	out := json.NewEncoder(os.Stdout)
	out.Encode(map[string]any{
		"addr":         ln.Addr().String(),
		"uid":          hex.EncodeToString(state.Gen2UID()),
		"provisioning": hex.EncodeToString(prov[:]),
		"slot":         *slot,
	})
	for {
		c, err := ln.Accept()
		if err != nil {
			log.Fatal(err)
		}
		if err := serve(c, sensor, state, *frames, out); err != nil {
			log.Printf("connection: %v", err)
			if *once {
				os.Exit(1)
			}
		}
		c.Close()
		if *once {
			return
		}
	}
}

func serve(c net.Conn, g *libre2.Gen2Sensor, s *libre2.State, frames int, out *json.Encoder) error {
	r := bufio.NewReader(c)
	kick, err := r.ReadByte()
	if err != nil || kick != 0x20 {
		return fmt.Errorf("want kickoff 0x20, got %#x (%v)", kick, err)
	}
	if _, err := c.Write(g.Challenge()); err != nil {
		return err
	}
	req := make([]byte, libre2.Gen2AuthRequestSize)
	if _, err := io.ReadFull(r, req); err != nil {
		return err
	}
	resp, err := g.Accept(req)
	if err != nil {
		out.Encode(map[string]any{"auth": "rejected", "error": err.Error()})
		return err
	}
	if _, err := c.Write(resp); err != nil {
		return err
	}
	out.Encode(map[string]any{"auth": "ok"})
	lc := s.LifeCount(time.Now())
	for i := 0; i < frames; i++ {
		plain := s.Gen2PacketPlain(lc + i)
		f, err := g.Frame(uint16(i+1), plain)
		if err != nil {
			return err
		}
		if _, err := c.Write(f); err != nil {
			return err
		}
		out.Encode(map[string]any{"frame": i + 1, "plain": hex.EncodeToString(plain)})
	}
	return nil
}

type randReader struct{}

func (randReader) Read(p []byte) (int, error) {
	f, err := os.Open("/dev/urandom")
	if err != nil {
		return 0, err
	}
	defer f.Close()
	return io.ReadFull(f, p)
}
