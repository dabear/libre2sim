package libre2

import (
	"bytes"
	"encoding/binary"
	"os"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

func checkFRAMCRCs(t *testing.T, plain []byte) (header, body, footer bool) {
	t.Helper()
	if len(plain) != framSize {
		t.Fatalf("FRAM is %d bytes, want %d", len(plain), framSize)
	}
	return framRegionCRCValid(plain[0:24]), framRegionCRCValid(plain[24:320]), framRegionCRCValid(plain[320:344])
}

// Vector 1: decrypting LibreTransmitter's sample FRAMs yields valid CRCs.
func TestFRAMExamplesCRC(t *testing.T) {
	for _, c := range []struct {
		name            string
		uid, info, fram []byte
	}{
		{"Example", exampleUID, exampleInfo, exampleFRAM},
		{"Example2", example2UID, example2Info, example2FRAM},
	} {
		t.Run(c.name, func(t *testing.T) {
			p := cryptFRAM(c.uid, c.info, c.fram)
			h, b, f := checkFRAMCRCs(t, p)
			if !h || !b || !f {
				t.Fatalf("CRC header=%v body=%v footer=%v", h, b, f)
			}
			t.Logf("header %x, footer %x", p[:24], p[320:])
		})
	}
}

// The xDrip sample in FakeSensorPairingData does NOT decrypt with its own patch
// info, in Go or in LibreTransmitter's Swift (swift_vectors.json records
// headerCRCValid=false etc. for it). Only s1 = word(uid[5],uid[4]) + block +
// y depends on uid[4..5] and the info word, and every region validates when
// word(uid[5],uid[4]) + (infoWord^0x44) == 0xad57 (mod 2^16). With the sample's
// uid (… 00 a4 07 e0) that means patch info bytes 4..5 = 13 09, not 9c 16. So
// the FRAM and patch info in that sample (copied unchanged from xDrip's
// NFCReaderX.java) come from different scans or sensors; the port is fine.
func TestFRAMXDripSample(t *testing.T) {
	h, b, f := checkFRAMCRCs(t, cryptFRAM(xdripUID, xdripInfo, xdripFRAM))
	if h || b || f {
		t.Fatalf("expected the xDrip sample to fail with its own patch info, got header=%v body=%v footer=%v", h, b, f)
	}

	info := append([]byte(nil), xdripInfo...)
	info[4], info[5] = 0x13, 0x09
	p := cryptFRAM(xdripUID, info, xdripFRAM)
	h, b, f = checkFRAMCRCs(t, p)
	if !h || !b || !f {
		t.Fatalf("with patch info %x: CRC header=%v body=%v footer=%v", info, h, b, f)
	}
	t.Logf("validates with patch info bytes 4..5 = 13 09: header %x, footer %x", p[:24], p[320:])
}

// Vector 2: the BLE example decrypts to a packet with a valid CRC.
func TestBLEExample(t *testing.T) {
	pt, err := decryptPacket(bleUID, blePacket)
	if err != nil {
		t.Fatal(err)
	}
	age := binary.LittleEndian.Uint16(pt[40:42])
	t.Logf("plaintext %x, age %d min (%.1f days)", pt, age, float64(age)/1440)

	if _, err := decryptPacket(exampleUID, blePacket); err == nil {
		t.Error("BLE example decrypted under the wrong UID")
	}
}

// Vector 3: round trips.
func TestFRAMRoundTrip(t *testing.T) {
	for _, fram := range [][]byte{exampleFRAM, example2FRAM, xdripFRAM} {
		p := cryptFRAM(exampleUID, exampleInfo, fram)
		if got := cryptFRAM(exampleUID, exampleInfo, p); !bytes.Equal(got, fram) {
			t.Fatal("cryptFRAM(cryptFRAM(x)) != x")
		}
	}
	// Build a plaintext from scratch, fix its CRCs, encrypt, decrypt.
	plain := cryptFRAM(example2UID, example2Info, example2FRAM)
	plain[4] = 0x02 // tweak the header, body and footer
	plain[100] ^= 0xff
	plain[330] ^= 0x55
	setFramRegionCRC(plain[0:24])
	setFramRegionCRC(plain[24:320])
	setFramRegionCRC(plain[320:344])
	enc := cryptFRAM(bleUID, example2Info, plain)
	h, b, f := checkFRAMCRCs(t, cryptFRAM(bleUID, example2Info, enc))
	if !h || !b || !f {
		t.Fatalf("CRC header=%v body=%v footer=%v", h, b, f)
	}
}

func TestPacketRoundTrip(t *testing.T) {
	pt, err := decryptPacket(bleUID, blePacket)
	if err != nil {
		t.Fatal(err)
	}
	if got := encryptPacket(bleUID, blePacket[0], blePacket[1], pt); !bytes.Equal(got, blePacket) {
		t.Fatalf("re-encrypting the BLE example: got %x, want %x", got, blePacket)
	}

	pt2 := make([]byte, packetPlainSz)
	for i := range pt2[:42] {
		pt2[i] = byte(i * 7)
	}
	c := libreCRC16(pt2[:42])
	pt2[42], pt2[43] = byte(c>>8), byte(c)
	for _, n := range [][2]byte{{0, 0}, {0xff, 0xff}, {0x12, 0x34}} {
		pkt := encryptPacket(exampleUID, n[0], n[1], pt2)
		got, err := decryptPacket(exampleUID, pkt)
		if err != nil || !bytes.Equal(got, pt2) {
			t.Fatalf("nonce %x: got %x, %v", n, got, err)
		}
		pkt[10] ^= 1
		if _, err := decryptPacket(exampleUID, pkt); err == nil {
			t.Fatalf("nonce %x: corrupted packet passed the CRC", n)
		}
	}
}

var swiftTableRE = regexp.MustCompile(`(?s)crc16table: \[UInt16\] = \[(.*?)\]`)

func parseSwiftTable(t *testing.T, path string) []uint16 {
	t.Helper()
	src, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	m := swiftTableRE.FindSubmatch(src)
	if m == nil {
		t.Fatalf("%s: no crc16table", path)
	}
	var tab []uint16
	for _, f := range strings.Split(string(m[1]), ",") {
		v, err := strconv.ParseUint(strings.ReplaceAll(strings.TrimSpace(f), "_", ""), 10, 16)
		if err != nil {
			t.Fatalf("%s: %v", path, err)
		}
		tab = append(tab, uint16(v))
	}
	return tab
}

// Crc.crc16(seed: 0xffff) and PreLibre2.swift's private crc16 run the same
// loop (crc>>8 ^ table[(crc^b)&0xff] from 0xffff, reverse 16 bits, byteSwapped);
// this checks that their tables are also identical, and equal to ours.
func TestCRCTablesMatchSwift(t *testing.T) {
	for _, path := range []string{"../tools/swiftvectors/CRC.swift", "../tools/swiftvectors/PreLibre2.swift"} {
		tab := parseSwiftTable(t, path)
		if len(tab) != 256 {
			t.Fatalf("%s: table has %d entries", path, len(tab))
		}
		for i, v := range tab {
			if v != crc16Table[i] {
				t.Fatalf("%s: table[%d] = %d, ours %d", path, i, v, crc16Table[i])
			}
		}
	}
}

func TestEnableStreamingParamsLayout(t *testing.T) {
	p := enableStreamingParams(bleUID, example2Info, 42)
	d := enableStreamingD(bleUID, example2Info, 42)
	want := append([]byte{0x1e, 42, 0, 0, 0}, d[:]...)
	if !bytes.Equal(p, want) {
		t.Fatalf("got %x, want %x", p, want)
	}
}
