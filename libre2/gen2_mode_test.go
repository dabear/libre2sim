// SPDX-License-Identifier: GPL-3.0-or-later

package libre2

import (
	"bytes"
	"encoding/binary"
	"errors"
	"sync"
	"testing"
	"time"
)

// gen2Host is the app's side of a Gen2 exchange, written from Juggluco's
// Gen2.java and protocol.c (l2g2_build_nfc_auth_request,
// l2g2_accept_auth_response, l2g2_read_command_reply,
// l2g2_decrypt_nfc_blocks, l2g2_build_ble_auth_request). Juggluco's own C is
// checked against the BLE side in tools/gen2bridge.
type gen2Host struct {
	t         *testing.T
	s         *Sensor
	uid       []byte
	sensorKey [speckKeySize]byte
	slot      int

	// the current exchange
	salt         [3]byte
	prefix       [4]byte
	epoch        [2]byte
	counter      uint32
	sensorRandom []byte
	hostRandom   []byte
}

func newGen2Host(t *testing.T, s *Sensor, st *State) *gen2Host {
	t.Helper()
	h := &gen2Host{t: t, s: s, uid: st.uid(), hostRandom: []byte{9, 8, 7, 6, 5, 4, 3}}
	return h
}

func (h *gen2Host) custom(params ...byte) ([]byte, error) {
	return h.s.NFCCustom(cmdA1, params, time.Now())
}

func (h *gen2Host) mustCustom(params ...byte) []byte {
	h.t.Helper()
	r, err := h.custom(params...)
	if err != nil {
		h.t.Fatalf("A1 %x: %v", params, err)
	}
	return r
}

// readSlot is A1 22: Juggluco reads the slot at raw[3..4], CoreNFC's [2..3].
func (h *gen2Host) readSlot() {
	r := h.mustCustom(gen2NFCReadRootSlot)
	if len(r) < 4 {
		h.t.Fatalf("root slot reply %x", r)
	}
	h.slot = int(r[2]) | int(r[3])<<8
	k, err := deriveSensorKey(h.slot, h.uid)
	if err != nil {
		h.t.Fatal(err)
	}
	h.sensorKey = k
}

// authRequest reads a challenge and returns the authenticated command cmd.
func (h *gen2Host) authRequest(cmd byte) []byte {
	h.t.Helper()
	c := h.mustCustom(gen2NFCReadChallenge)
	if len(c) != 25 || !packetCRCValid(c[:25]) {
		h.t.Fatalf("challenge %x: bad length or CRC", c)
	}
	h.sensorRandom = append([]byte(nil), c[:7]...)
	h.counter = binary.LittleEndian.Uint32(c[7:])
	copy(h.salt[:], c[14:17])
	copy(h.prefix[:], c[17:21])
	copy(h.epoch[:], c[21:23])
	pt := append(append(append([]byte(nil), h.hostRandom...), ^cmd), h.sensorRandom...)
	rk := newSpeckKey(h.sensorKey[:])
	cryptCTR(&rk, authNonce(h.salt, h.counter, TransportNFC, 0), pt)
	return append([]byte{cmd}, pt...)
}

// open decrypts a 0x1F reply (header ‖ 23 ‖ CRC) into the data key.
func (h *gen2Host) open(r []byte) [speckKeySize]byte {
	h.t.Helper()
	if len(r) != 33 || !packetCRCValid(r[8:]) {
		h.t.Fatalf("0x1F reply %x", r)
	}
	pt := append([]byte(nil), r[8:31]...)
	rk := newSpeckKey(h.sensorKey[:])
	cryptCTR(&rk, authNonce(h.salt, h.counter, TransportNFC, 1), pt)
	if !bytes.Equal(pt[:7], h.hostRandom) {
		h.t.Fatal("0x1F reply: host random not echoed")
	}
	k, _ := deriveSessionKey(h.slot, pt[7:23])
	return k
}

// commandReply decrypts an authenticated command's reply into its payload.
func (h *gen2Host) commandReply(r []byte, size int) []byte {
	h.t.Helper()
	enc := size + 14
	if len(r) != 8+enc+2 || !packetCRCValid(r[8:]) {
		h.t.Fatalf("command reply %x", r)
	}
	pt := append([]byte(nil), r[8:8+enc]...)
	rk := newSpeckKey(h.sensorKey[:])
	cryptCTR(&rk, authNonce(h.salt, h.counter, TransportNFC, 1), pt)
	if !bytes.Equal(pt[:7], h.sensorRandom) || !bytes.Equal(pt[7+size:], h.hostRandom) {
		h.t.Fatal("command reply: randoms not echoed")
	}
	return pt[7 : 7+size]
}

// readFRAM is readAuthenticatedFram for the whole 344-byte FRAM.
func (h *gen2Host) readFRAM() []byte {
	h.t.Helper()
	key := h.open(h.mustCustom(h.authRequest(gen2NFCAuthFRAMRead)...))
	rk := newSpeckKey(key[:])
	var fram []byte
	for b := 0; b < framBlocks; b += 3 {
		n := 3
		if framBlocks-b < n {
			n = framBlocks - b
		}
		r := h.mustCustom(gen2NFCReadBlocks, byte(b), byte(n-1))
		if len(r) != 8+n*8 {
			h.t.Fatalf("A1 21 %d: %d bytes", b, len(r))
		}
		for i := 0; i < n; i++ {
			var nonce [8]byte
			nonce[1] = byte(b + i)
			copy(nonce[2:6], h.prefix[:])
			nonce[5] &= 0x7f
			copy(nonce[6:], h.epoch[:])
			ks := rk.encrypt(nonce[:])
			for j := 0; j < 8; j++ {
				fram = append(fram, r[8+i*8+j]^ks[j])
			}
		}
	}
	return fram
}

// enableStreaming is Gen2.enableStreaming: it returns the BLE address (as
// sent) and the 10-byte provisioning.
func (h *gen2Host) enableStreaming() (addr []byte, prov Gen2Provisioning) {
	h.t.Helper()
	reply := h.commandReply(h.mustCustom(h.authRequest(gen2NFCEnableStreaming)...), 9)
	copy(prov[0:4], h.prefix[:])
	copy(prov[4:7], h.salt[:])
	copy(prov[7:10], reply[6:9])
	return reply[:6], prov
}

func gen2Sensor(t *testing.T) (*Sensor, *State) {
	t.Helper()
	st := bleState(t, time.Hour)
	s := NewSensor(st, t.TempDir()+"/state.json")
	if !s.SetGen2(true) {
		t.Fatal("SetGen2 reported no change")
	}
	return s, st
}

func TestGen2NFCPairing(t *testing.T) {
	s, st := gen2Sensor(t)
	h := newGen2Host(t, s, st)

	if pi := h.mustCustom(); pi[2] != 0x39 || pi[0] != hexBytes(st.PatchInfo)[0] {
		t.Fatalf("patch info %x: want security generation 2 (0x39)", pi)
	}
	h.readSlot()
	if h.slot != 1 {
		t.Fatalf("root slot %d", h.slot)
	}

	fram := h.readFRAM()
	if want := st.plainFRAM(time.Now()); !bytes.Equal(fram, want) {
		t.Fatalf("decrypted FRAM differs:\n got %x\nwant %x", fram, want)
	}
	for _, r := range [][2]int{{0, 24}, {24, 320}, {320, 344}} {
		if !framRegionCRCValid(fram[r[0]:r[1]]) {
			t.Errorf("FRAM region %v CRC", r)
		}
	}

	if s.Status()["streamingEnabled"] != false {
		t.Fatal("streaming enabled before 0x1E")
	}
	addr, prov := h.enableStreaming()
	if !bytes.Equal(addr, st.macLE()) {
		t.Errorf("address %x, want %x", addr, st.macLE())
	}
	stream := hexBytes(st.Gen2Stream)
	if !bytes.Equal(prov[:], stream[:10]) {
		t.Errorf("provisioning %x, want %x", prov, stream[:10])
	}
	if s.Status()["streamingEnabled"] != true || !s.Advertising(time.Now()) {
		t.Fatal("streaming not enabled after 0x1E")
	}
}

func TestGen2NFCRefusals(t *testing.T) {
	s, st := gen2Sensor(t)
	h := newGen2Host(t, s, st)
	h.readSlot()
	nfcCode := func(err error) byte {
		var e *NFCError
		if !errors.As(err, &e) {
			t.Fatalf("error %v, want an NFCError", err)
		}
		return e.Code
	}

	// The Gen1 enable-streaming request and standard block read.
	code, params := EnableStreamingCommand(st.uid(), st.patchInfo(), 42)
	if _, err := s.NFCCustom(code, params, time.Now()); nfcCode(err) != nfcErrFormat {
		t.Errorf("Gen1 enable streaming: %v", err)
	}
	if _, err := s.NFCReadBlocks(0, 3); nfcCode(err) != nfcErrNotSupported {
		t.Errorf("standard read: %v", err)
	}
	// A1 21 before an authenticated 0x1F.
	if _, err := h.custom(gen2NFCReadBlocks, 0, 2); nfcCode(err) != nfcErrBlock {
		t.Errorf("unauthenticated A1 21: %v", err)
	}
	// A tampered request, and one replayed after its challenge was used.
	req := h.authRequest(gen2NFCEnableStreaming)
	bad := append([]byte(nil), req...)
	bad[10] ^= 1 // inside the sensor-random echo
	if _, err := h.custom(bad...); nfcCode(err) != nfcErrFormat {
		t.Errorf("tampered: %v", err)
	}
	if _, err := h.custom(req...); nfcCode(err) != nfcErrFormat {
		t.Errorf("replayed: %v", err)
	}
	// Activation over NFC is not emulated.
	if _, err := h.custom(h.authRequest(gen2NFCActivate)...); nfcCode(err) != nfcErrNotSupported {
		t.Errorf("0x1B: %v", err)
	}
	if s.Status()["streamingEnabled"] != false {
		t.Fatal("a refused command enabled streaming")
	}
}

func TestGen2TogglePersists(t *testing.T) {
	st := bleState(t, time.Hour)
	path := t.TempDir() + "/state.json"
	s := NewSensor(st, path)
	gen1Info := hexBytes(st.PatchInfo)

	// Gen1 streaming, then the switch: streaming goes, the state file has it.
	code, params := EnableStreamingCommand(st.uid(), st.patchInfo(), 42)
	if _, err := s.NFCCustom(code, params, time.Now()); err != nil {
		t.Fatal(err)
	}
	if !s.SetGen2(true) || s.SetGen2(true) {
		t.Fatal("SetGen2 change reporting")
	}
	loaded, err := LoadState(path)
	if err != nil {
		t.Fatal(err)
	}
	if !loaded.Gen2 || loaded.StreamingCode != nil || loaded.Gen2RootSlot != 1 || len(hexBytes(loaded.Gen2Stream)) != gen2StreamSize {
		t.Fatalf("saved state: gen2 %v code %v slot %d stream %q", loaded.Gen2, loaded.StreamingCode, loaded.Gen2RootSlot, loaded.Gen2Stream)
	}
	if loaded.PatchInfo != st.PatchInfo {
		t.Fatal("the stored patch info changed; only the reported one should")
	}

	// A restart restores Gen2 with the same stream parameters.
	s2 := NewSensor(loaded, path)
	h := newGen2Host(t, s2, loaded)
	h.readSlot()
	_, prov := h.enableStreaming()
	if !bytes.Equal(prov[:], hexBytes(st.Gen2Stream)[:10]) {
		t.Fatal("stream parameters changed across a restart")
	}

	// Back to Gen1: the Gen1 patch info, and Gen2 streaming is gone.
	if !s2.SetGen2(false) {
		t.Fatal("SetGen2(false) reported no change")
	}
	if pi, _ := s2.NFCCustom(cmdA1, nil, time.Now()); !bytes.Equal(pi, gen1Info) {
		t.Fatalf("Gen1 patch info %x, want %x", pi, gen1Info)
	}
	if s2.Status()["streamingEnabled"] != false || s2.Advertising(time.Now()) {
		t.Fatal("still streaming after switching back")
	}
	loaded, _ = LoadState(path)
	if loaded.Gen2 || loaded.Gen2StreamingEnabled {
		t.Fatal("Gen1 not saved")
	}
}

// gen2Link records notifies on both characteristics.
type gen2Link struct {
	mu     sync.Mutex
	on     map[Char]bool
	got    map[Char][][]byte
	closed bool
}

func (l *gen2Link) Addr() string { return "test" }
func (l *gen2Link) Notify(c Char, b []byte) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if !l.on[c] {
		return errors.New("not subscribed")
	}
	l.got[c] = append(l.got[c], append([]byte(nil), b...))
	return nil
}
func (l *gen2Link) Respond(c Char, b []byte) {}
func (l *gen2Link) Close() {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.closed = true
}

func (l *gen2Link) await(t *testing.T, c Char, n int) [][]byte {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		l.mu.Lock()
		got, closed := append([][]byte(nil), l.got[c]...), l.closed
		l.mu.Unlock()
		if n < 0 && closed || n >= 0 && len(got) >= n {
			return got
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("waiting for %d notifies on %s", n, c)
	return nil
}

func newGen2Rig(t *testing.T, s *Sensor) (*gen2Link, *handler) {
	link := &gen2Link{on: map[Char]bool{}, got: map[Char][][]byte{}}
	clock := &fakeClock{t: time.Now()}
	h := newHandler(s, link, handlerTiming{tick: 2 * time.Millisecond, idle: unlockTimeout, now: clock.now})
	t.Cleanup(h.Closed)
	return link, h
}

func (l *gen2Link) subscribe(h *handler, c Char) {
	l.mu.Lock()
	l.on[c] = true
	l.mu.Unlock()
	h.Subscribed(c, true)
}

func TestGen2BLELogin(t *testing.T) {
	s, st := gen2Sensor(t)
	host := newGen2Host(t, s, st)
	host.readSlot()
	_, prov := host.enableStreaming()

	link, h := newGen2Rig(t, s)
	link.subscribe(h, CharWrite)
	link.subscribe(h, CharNotify)
	h.Write(CharWrite, []byte{0x20})
	ch := link.await(t, CharWrite, 1)[0]
	if len(ch) != 14 {
		t.Fatalf("challenge %x", ch)
	}

	// l2g2_build_ble_auth_request.
	counter := binary.LittleEndian.Uint32(ch[7:])
	pt := append(append(append(append([]byte(nil), host.hostRandom...), ^byte(0x1f)), prov[7:10]...), ch[:7]...)
	rk := newSpeckKey(host.sensorKey[:])
	cryptCTR(&rk, authNonce(prov.salt(), counter, TransportBLE, 0), pt)
	h.Write(CharWrite, append([]byte{0x1f}, pt...))

	login := link.await(t, CharWrite, 3)
	if len(login[1]) != 7 || len(login[2]) != 18 {
		t.Fatalf("response notifies of %d and %d bytes, want 7 and 18", len(login[1]), len(login[2]))
	}
	resp := append(append([]byte(nil), login[1]...), login[2]...)
	cryptCTR(&rk, authNonce(prov.salt(), counter, TransportBLE, 1), resp[:23])
	if !bytes.Equal(resp[:7], host.hostRandom) {
		t.Fatal("response: host random not echoed")
	}
	key, _ := deriveSessionKey(host.slot, resp[7:23])

	got := link.await(t, CharNotify, 3)
	if len(got[0]) != 20 || len(got[1]) != 18 || len(got[2]) != 8 {
		t.Fatalf("frame notifies of %d, %d, %d bytes, want 20, 18, 8", len(got[0]), len(got[1]), len(got[2]))
	}
	frame := append(append(append([]byte(nil), got[0]...), got[1]...), got[2]...)
	plain, err := decryptBLEFrame(key, prov.streamPrefix(), frame)
	if err != nil {
		t.Fatal(err)
	}
	lc := st.lifeCount(time.Now())
	if want := st.packetPlain(lc); !bytes.Equal(plain[2:], want) {
		t.Fatalf("frame plaintext %x, want minute %d's %x", plain[2:], lc, want)
	}
}

func TestGen2BLERefusals(t *testing.T) {
	s, st := gen2Sensor(t)
	host := newGen2Host(t, s, st)
	host.readSlot()

	// Streaming not enabled yet: the sensor does not advertise, and a
	// challenge request is refused.
	if s.Advertising(time.Now()) {
		t.Fatal("advertising before Gen2 enable streaming")
	}
	link, h := newGen2Rig(t, s)
	link.subscribe(h, CharWrite)
	h.Write(CharWrite, []byte{0x20})
	link.await(t, CharWrite, -1)

	host.enableStreaming()
	// No F001 notifications: refused.
	link, h = newGen2Rig(t, s)
	h.Write(CharWrite, []byte{0x20})
	link.await(t, CharWrite, -1)

	// A Gen1 unlock on a Gen2 sensor: refused.
	link, h = newGen2Rig(t, s)
	link.subscribe(h, CharWrite)
	p := unlockPayload(st.uid(), hexBytes(st.PatchInfo), 42, 1)
	h.Write(CharWrite, p[:])
	link.await(t, CharWrite, -1)

	// A wrong auth request: refused.
	link, h = newGen2Rig(t, s)
	link.subscribe(h, CharWrite)
	h.Write(CharWrite, []byte{0x20})
	link.await(t, CharWrite, 1)
	h.Write(CharWrite, make([]byte, 19))
	link.await(t, CharWrite, -1)
}

// With the toggle off, the Gen2 login is refused and Gen1 still streams.
func TestGen2OffIsGen1(t *testing.T) {
	s, st := streamingSensor(t)
	if s.Gen2() {
		t.Fatal("Gen2 on by default")
	}
	link, h := newGen2Rig(t, s)
	link.subscribe(h, CharWrite)
	h.Write(CharWrite, []byte{0x20})
	link.await(t, CharWrite, -1)

	link, h = newGen2Rig(t, s)
	link.subscribe(h, CharNotify)
	p := unlockPayload(st.uid(), st.patchInfo(), 42, 1)
	h.Write(CharWrite, p[:])
	got := link.await(t, CharNotify, 3)
	if len(got[0]) != 20 || len(got[1]) != 20 || len(got[2]) != 6 {
		t.Fatalf("Gen1 packet as %d, %d, %d", len(got[0]), len(got[1]), len(got[2]))
	}
}
