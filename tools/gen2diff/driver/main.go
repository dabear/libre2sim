// SPDX-License-Identifier: GPL-3.0-or-later

// Command gen2diff compares two Gen2 client implementations byte for byte on
// random inputs: Juggluco's protocol.c (juggluco_peer.c) and LibreTransmitter's
// Libre2Gen2.swift (lt_peer.swift). libre2sim's Gen2 sensor makes the
// challenges, replies and frames, so every request is also checked by a sensor
// and every reply is one a sensor would send. Build and run it with run.sh.
//
//	gen2diff [-cases N] [-seed S] jugglucoPeer ltPeer
//
// Each case checks, for a random UID, root slot and provisioning:
//   - the sensor key (KEY)
//   - the BLE auth request for the sensor's challenge, which the sensor must
//     accept (BLEREQ), and that a reply with a damaged echo is refused by both
//     (BLEOPEN)
//   - the session key from the sensor's reply (BLEOPEN)
//   - frames the sensor encrypts, which both must decrypt to its plaintext,
//     and random frames under random keys (BLEDEC, BLEDECK)
//   - the NFC auth request for a random challenge with a valid CRC, and that a
//     challenge with a bad CRC is refused by both (NFCREQ)
//   - FRAM block decryption under a random key (NFCDEC)
//   - the CRC of random data of odd and even length (CRC)
package main

import (
	"bufio"
	"encoding/hex"
	"flag"
	"fmt"
	"io"
	"math/rand"
	"os"
	"os/exec"
	"sort"
	"strings"

	"libre2sim/libre2"
)

type peer struct {
	name string
	in   io.Writer
	out  *bufio.Scanner
}

func start(name, path string) *peer {
	cmd := exec.Command(path)
	in, err := cmd.StdinPipe()
	check(err)
	out, err := cmd.StdoutPipe()
	check(err)
	cmd.Stderr = os.Stderr
	check(cmd.Start())
	sc := bufio.NewScanner(out)
	sc.Buffer(make([]byte, 1<<16), 1<<16)
	return &peer{name, in, sc}
}

// ask sends one command and returns the answer's bytes, or ok=false with the
// reason when the peer refused.
func (p *peer) ask(cmd string) (b []byte, ok bool, reason string) {
	_, err := fmt.Fprintln(p.in, cmd)
	check(err)
	if !p.out.Scan() {
		fmt.Fprintf(os.Stderr, "%s quit on %q\n", p.name, cmd)
		os.Exit(2)
	}
	line := p.out.Text()
	if strings.HasPrefix(line, "OK ") {
		b, err := hex.DecodeString(line[3:])
		check(err)
		return b, true, ""
	}
	return nil, false, strings.TrimPrefix(line, "ERR ")
}

func check(err error) {
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}
}

var (
	rng      *rand.Rand
	checks   = map[string]int{}
	failures = map[string]int{}
	shown    int
)

func randBytes(n int) []byte {
	b := make([]byte, n)
	rng.Read(b)
	return b
}

func h(b []byte) string { return hex.EncodeToString(b) }

func fail(kind, format string, a ...interface{}) {
	failures[kind]++
	if shown < 10 {
		shown++
		fmt.Printf("MISMATCH %s: %s\n", kind, fmt.Sprintf(format, a...))
	}
}

// same sends cmd to both peers and requires the same answer, and returns it.
// want, when not nil, is what both must answer.
func same(kind, cmd string, j, l *peer, want []byte) ([]byte, bool) {
	checks[kind]++
	jb, jok, jr := j.ask(cmd)
	lb, lok, lr := l.ask(cmd)
	switch {
	case !jok || !lok:
		fail(kind, "%s\n  juggluco ok=%v %s %s\n  lt       ok=%v %s %s", cmd, jok, h(jb), jr, lok, h(lb), lr)
		return nil, false
	case h(jb) != h(lb):
		fail(kind, "%s\n  juggluco %s\n  lt       %s", cmd, h(jb), h(lb))
		return nil, false
	case want != nil && h(jb) != h(want):
		fail(kind, "%s\n  both  %s\n  want  %s", cmd, h(jb), h(want))
		return nil, false
	}
	return jb, true
}

// refused sends cmd to both peers and requires both to refuse it.
func refused(kind, cmd string, j, l *peer) {
	checks[kind]++
	jb, jok, _ := j.ask(cmd)
	lb, lok, _ := l.ask(cmd)
	if jok || lok {
		fail(kind, "%s\n  juggluco ok=%v %s\n  lt       ok=%v %s", cmd, jok, h(jb), lok, h(lb))
	}
}

// gen2CRC is libre2's: odd-length data padded with a zero byte, the result
// stored after the data as matches_crc reads it.
func gen2CRC(data []byte) [2]byte {
	if len(data)%2 == 1 {
		data = append(append([]byte(nil), data...), 0)
	}
	crc := uint16(0xffff)
	for _, v := range data {
		crc ^= uint16(v)
		for i := 0; i < 8; i++ {
			if crc&1 != 0 {
				crc = crc>>1 ^ 0x8408
			} else {
				crc >>= 1
			}
		}
	}
	var rev uint16
	for i := 0; i < 16; i++ {
		rev = rev<<1 | crc&1
		crc >>= 1
	}
	return [2]byte{byte(rev), byte(rev >> 8)}
}

func main() {
	cases := flag.Int("cases", 1000, "number of random cases")
	seed := flag.Int64("seed", 1, "random seed")
	flag.Parse()
	if flag.NArg() != 2 {
		fmt.Fprintln(os.Stderr, "usage: gen2diff [-cases N] [-seed S] jugglucoPeer ltPeer")
		os.Exit(2)
	}
	rng = rand.New(rand.NewSource(*seed))
	j := start("juggluco", flag.Arg(0))
	l := start("lt", flag.Arg(1))

	for c := 0; c < *cases; c++ {
		uid := randBytes(8)
		slot := 1 + rng.Intn(2)

		same("KEY", fmt.Sprintf("KEY %d %s", slot, h(uid)), j, l, nil)
		refused("KEY bad slot", fmt.Sprintf("KEY %d %s", []int{0, 3}[rng.Intn(2)], h(uid)), j, l)

		// BLE: request, refused reply, session key, frames.
		var prov libre2.Gen2Provisioning
		copy(prov[:], randBytes(len(prov)))
		sensor, err := libre2.NewGen2Sensor(uid, slot, prov, randBytes)
		check(err)
		challenge := sensor.Challenge()
		hostRandom := randBytes(7)
		req := fmt.Sprintf("BLEREQ %d %s %s %s %s", slot, h(uid), h(prov[:]), h(challenge), h(hostRandom))
		request, ok := same("BLEREQ", req, j, l, nil)
		if !ok {
			continue
		}
		checks["BLEREQ sensor accepts"]++
		reply, err := sensor.Accept(request)
		if err != nil {
			fail("BLEREQ sensor accepts", "%s: %v", req, err)
			continue
		}
		bad := append([]byte(nil), reply...)
		bad[rng.Intn(7)] ^= byte(1 + rng.Intn(255))
		refused("BLEOPEN damaged echo", "BLEOPEN "+h(bad), j, l)
		// The refused reply must not have spoiled the exchange.
		if _, ok := same("BLEOPEN", "BLEOPEN "+h(reply), j, l, nil); !ok {
			continue
		}
		for f := 0; f < 3; f++ {
			plain := randBytes(44)
			crc := gen2CRC(plain[:42])
			plain[42], plain[43] = crc[0], crc[1]
			seq := uint16(rng.Intn(1 << 16))
			frame, err := sensor.Frame(seq, plain)
			check(err)
			same("BLEDEC sensor frame", "BLEDEC "+h(frame), j, l, plain)
		}
		same("BLEDECK random", fmt.Sprintf("BLEDECK %s %s", h(randBytes(16)), h(randBytes(46))), j, l, nil)

		// NFC: request for a challenge with a valid CRC, and one with a bad CRC.
		nchal := randBytes(25)
		ncrc := gen2CRC(nchal[:23])
		nchal[23], nchal[24] = ncrc[0], ncrc[1]
		cmd := []byte{0x1f, 0x1e, 0x1b, byte(rng.Intn(256))}[rng.Intn(4)]
		nreq := fmt.Sprintf("NFCREQ %d %s %02x %s %s", slot, h(uid), cmd, h(nchal), h(randBytes(7)))
		if _, ok := same("NFCREQ", nreq, j, l, nil); ok {
			count := 1 + rng.Intn(3)
			same("NFCDEC", fmt.Sprintf("NFCDEC %s %d %d %s", h(randBytes(16)), rng.Intn(256-count), count, h(randBytes(8*count))), j, l, nil)
		}
		badchal := append([]byte(nil), nchal...)
		badchal[rng.Intn(25)] ^= byte(1 + rng.Intn(255))
		refused("NFCREQ bad CRC", fmt.Sprintf("NFCREQ %d %s %02x %s %s", slot, h(uid), cmd, h(badchal), h(randBytes(7))), j, l)

		same("CRC", "CRC "+h(randBytes(1+rng.Intn(60))), j, l, nil)
	}

	kinds := make([]string, 0, len(checks))
	for k := range checks {
		kinds = append(kinds, k)
	}
	sort.Strings(kinds)
	total := 0
	for _, k := range kinds {
		fmt.Printf("%-24s %6d checks, %d mismatches\n", k, checks[k], failures[k])
		total += failures[k]
	}
	if total > 0 {
		fmt.Printf("FAIL: %d mismatches (seed %d)\n", total, *seed)
		os.Exit(1)
	}
	fmt.Printf("PASS: %d cases, seed %d\n", *cases, *seed)
}
