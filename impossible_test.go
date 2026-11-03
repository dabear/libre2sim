// Adapted from libre3sim's impossible_test.go (github.com/LoopKit/libre3sim
// @ ccd9a91, kept in ref/libre3sim/impossible_test.go.txt), MIT License,
// Copyright (c) 2026 LoopKit Authors.

package main

import (
	"bufio"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"math"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	"libre2sim/libre2"
)

// fakeApp is ImpossiBLE's library end of the socket, doing what
// LibreTransmitter does through it.
type fakeApp struct {
	t    *testing.T
	conn net.Conn
	in   chan map[string]interface{}
}

func newFakeApp(t *testing.T, conn net.Conn) *fakeApp {
	a := &fakeApp{t: t, conn: conn, in: make(chan map[string]interface{}, 100)}
	go func() {
		defer close(a.in)
		sc := bufio.NewScanner(conn)
		for sc.Scan() {
			var msg map[string]interface{}
			if err := json.Unmarshal(sc.Bytes(), &msg); err != nil {
				msg = map[string]interface{}{"type": "unparsable", "line": sc.Text()}
			}
			a.in <- msg
		}
	}()
	return a
}

func (a *fakeApp) send(msg map[string]interface{}) {
	b, _ := json.Marshal(msg)
	a.conn.Write(append(b, '\n'))
}

// next returns the next message, or nil once d passes or the socket closes.
func (a *fakeApp) next(d time.Duration) map[string]interface{} {
	select {
	case msg := <-a.in:
		if msg["type"] == "unparsable" {
			a.t.Fatalf("bad message %q", msg["line"])
		}
		return msg
	case <-time.After(d):
		return nil
	}
}

func (a *fakeApp) await(kind string) map[string]interface{} {
	a.t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		msg := a.next(time.Until(deadline))
		if msg == nil {
			break
		}
		if msg["type"] == kind {
			return msg
		}
	}
	a.t.Fatalf("no %s", kind)
	return nil
}

func TestScanConnectStream(t *testing.T) {
	// A Unix socket path must fit in 104 bytes, so not t.TempDir().
	dir, err := os.MkdirTemp("", "l2s")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)
	st, err := libre2.NewState(15, time.Hour, 3*time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	uid, _ := hex.DecodeString(st.UID)
	info, _ := hex.DecodeString(st.PatchInfo)
	sensor := libre2.NewSensor(st, filepath.Join(dir, "state.json"))
	sock := filepath.Join(dir, "s.sock")
	r, err := NewSimRadio(sensor, sock)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Stop()
	go r.Run()

	conn, err := net.Dial("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	app := newFakeApp(t, conn)
	app.send(map[string]interface{}{"type": "hello", "bundleId": "test.app", "pid": 0})
	app.send(map[string]interface{}{"type": "scan", "services": []string{"FDE3"}})

	// Until an NFC scan enables streaming the sensor is silent.
	for until := time.Now().Add(300 * time.Millisecond); time.Now().Before(until); {
		if msg := app.next(time.Until(until)); msg != nil && msg["type"] == "didDiscover" {
			t.Fatal("advertising before streaming was enabled")
		}
	}
	code, params := libre2.EnableStreamingCommand(uid, info, 42)
	if _, err := sensor.NFCCustom(code, params, time.Now()); err != nil {
		t.Fatal(err)
	}

	// LibreTransmitter's match: peripheral.name == the MAC as uppercase hex.
	seen := app.await("didDiscover")
	if seen["name"] != st.AdvertisedName() {
		t.Fatalf("advertised %v, want %s", seen["name"], st.AdvertisedName())
	}
	adv := seen["adv"].(map[string]interface{})
	if adv["kCBAdvDataLocalName"] != st.AdvertisedName() || adv["kCBAdvDataIsConnectable"] != true {
		t.Fatalf("advertisement %v", adv)
	}
	if s, _ := adv["kCBAdvDataServiceUUIDs"].([]interface{}); len(s) != 1 || s[0] != "FDE3" {
		t.Fatalf("advertised services %v", adv["kCBAdvDataServiceUUIDs"])
	}
	id := seen["id"].(string)

	// A connect to an earlier sensor is held, not refused.
	app.send(map[string]interface{}{"type": "connect", "id": "6A0E8C1B-0000-4000-8000-000000000000"})
	app.send(map[string]interface{}{"type": "stopScan"})
	app.send(map[string]interface{}{"type": "connect", "id": id})
	if c := app.await("didConnect"); c["id"] != id {
		t.Fatalf("connected %v", c["id"])
	}
	if sensor.Advertising(time.Now()) {
		t.Fatal("advertising while connected")
	}

	app.send(map[string]interface{}{"type": "discoverServices", "id": id, "services": []string{"FDE3"}})
	services := app.await("didDiscoverServices")["services"].([]interface{})
	if len(services) != 1 || services[0].(map[string]interface{})["uuid"] != "FDE3" {
		t.Fatalf("services %v", services)
	}
	service := services[0].(map[string]interface{})
	app.send(map[string]interface{}{"type": "discoverCharacteristics", "id": id, "serviceId": service["id"],
		"characteristics": []string{"F001", "F002"}})
	chars := app.await("didDiscoverCharacteristics")["characteristics"].([]interface{})
	if len(chars) != 2 {
		t.Fatalf("%d characteristics", len(chars))
	}
	write, notify := chars[0].(map[string]interface{}), chars[1].(map[string]interface{})
	if write["uuid"] != "F001" || write["properties"] != float64(propWrite) ||
		notify["uuid"] != "F002" || notify["properties"] != float64(propNotify) {
		t.Fatalf("characteristics %v", chars)
	}

	app.send(map[string]interface{}{"type": "setNotify", "id": id, "characteristicId": notify["id"], "enabled": true})
	if n := app.await("didUpdateNotification"); n["enabled"] != true || n["error"] != "" {
		t.Fatalf("setNotify answered %v", n)
	}
	unlock := libre2.StreamingUnlockPayload(uid, info, 42, 1)
	app.send(map[string]interface{}{"type": "write", "id": id, "characteristicId": write["id"],
		"value": base64.StdEncoding.EncodeToString(unlock), "writeType": 0})
	if w := app.await("didWriteValue"); w["characteristicId"] != write["id"] || w["error"] != "" {
		t.Fatalf("write answered %v", w)
	}

	// Libre2DirectTransmitter appends the notifies and parses 46 bytes.
	var buf []byte
	for _, size := range []int{20, 20, 6} {
		v := app.await("didUpdateValue")
		if v["characteristicId"] != notify["id"] || v["id"] != id {
			t.Fatalf("notify %v", v)
		}
		b, err := base64.StdEncoding.DecodeString(v["value"].(string))
		if err != nil || len(b) != size {
			t.Fatalf("notify of %d bytes (%v), want %d", len(b), err, size)
		}
		buf = append(buf, b...)
	}
	pt, err := libre2.DecryptPacket(uid, buf)
	if err != nil {
		t.Fatal(err)
	}
	d := libre2.ParseBLEData(pt)
	status := sensor.Status()
	lc := status["lifeCount"].(int)
	if !d.CRCVerified || d.Age < lc-1 || d.Age > lc || len(d.Trend) != 7 || len(d.History) != 3 {
		t.Fatalf("packet %+v at life count %d", d, lc)
	}
	if latest := d.Trend[len(d.Trend)-1]; d.Age == lc {
		cal := status["calibration"].(libre2.Calibration)
		if g := math.Round(cal.MgdlForRaw(latest.RawGlucose)); g != float64(status["glucose"].(uint16)) {
			t.Fatalf("packet says %v mg/dL, the sensor %v", g, status["glucose"])
		}
	}

	r.Disconnect()
	app.await("didDisconnect")
	if !sensor.Advertising(time.Now()) {
		t.Fatal("not advertising after the link dropped")
	}
}
