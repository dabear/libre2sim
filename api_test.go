package main

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"libre2sim/libre2"
)

func newTestServer(t *testing.T, age time.Duration) (*httptest.Server, *libre2.Sensor) {
	t.Helper()
	state, err := libre2.NewState(15, time.Hour, age)
	if err != nil {
		t.Fatal(err)
	}
	sensor := libre2.NewSensor(state, t.TempDir()+"/state.json")
	srv := httptest.NewServer(newAPI(sensor))
	t.Cleanup(srv.Close)
	return srv, sensor
}

// post sends body as SimulatedSensorPairingService does (a JSONSerialization
// dictionary, POST /nfc, Content-Type application/json) and decodes the
// reply.
func post(t *testing.T, srv *httptest.Server, body map[string]interface{}, reply interface{}) int {
	t.Helper()
	b, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := http.Post(srv.URL+"/nfc", "application/json", bytes.NewReader(b))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if err := json.NewDecoder(resp.Body).Decode(reply); err != nil {
		t.Fatalf("%v: decoding the reply: %v", body, err)
	}
	return resp.StatusCode
}

type nfcReply struct {
	Response *string `json:"response"`
}

// nfc is SimulatedSensorPairingService.nfc: 200 and {"response": hex}.
func nfc(t *testing.T, srv *httptest.Server, body map[string]interface{}) []byte {
	t.Helper()
	var r nfcReply
	if status := post(t, srv, body, &r); status != http.StatusOK || r.Response == nil {
		t.Fatalf("%v: HTTP %d, response %v", body, status, r.Response)
	}
	b, err := hex.DecodeString(*r.Response)
	if err != nil {
		t.Fatalf("%v: %v", body, err)
	}
	return b
}

type errorReply struct {
	Error string `json:"error"`
	Code  *int   `json:"code"`
}

func reverse(b []byte) []byte {
	out := make([]byte, len(b))
	for i := range b {
		out[i] = b[len(b)-1-i]
	}
	return out
}

// The whole scan, as SimulatedSensorPairingService.scan makes it.
func TestNFCOverHTTP(t *testing.T) {
	srv, sensor := newTestServer(t, 3*time.Hour)

	sensorUID := reverse(nfc(t, srv, map[string]interface{}{"op": "identifier"}))
	if si := nfc(t, srv, map[string]interface{}{"op": "systemInfo"}); len(si) != 0 {
		t.Errorf("systemInfo response %x, want empty", si)
	}
	patchInfo := nfc(t, srv, map[string]interface{}{"op": "custom", "code": 0xA1, "parameters": ""})

	var fram []byte
	requests := 0
	for first := 0; first < 43; first += 3 {
		count := 3
		if 43-first < count {
			count = 43 - first
		}
		var r struct {
			Blocks []string `json:"blocks"`
		}
		if status := post(t, srv, map[string]interface{}{"op": "readBlocks", "first": first, "count": count}, &r); status != http.StatusOK {
			t.Fatalf("readBlocks %d+%d: HTTP %d", first, count, status)
		}
		if len(r.Blocks) != count {
			t.Fatalf("readBlocks %d+%d: %d blocks", first, count, len(r.Blocks))
		}
		for _, h := range r.Blocks {
			b, err := hex.DecodeString(h)
			if err != nil || len(b) != 8 {
				t.Fatalf("block %q: %v", h, err)
			}
			fram = append(fram, b...)
		}
		requests++
	}

	code, params := libre2.EnableStreamingCommand(sensorUID, patchInfo, 42)
	// Swift's Data.hexEncodedString(); upper case must work too.
	resp := nfc(t, srv, map[string]interface{}{"op": "custom", "code": int(code), "parameters": strings.ToUpper(hex.EncodeToString(params))})

	if len(sensorUID) != 8 || len(patchInfo) != 6 || len(fram) != 344 || requests != 15 {
		t.Fatalf("uid %d, patch info %d, fram %d bytes in %d requests", len(sensorUID), len(patchInfo), len(fram), requests)
	}
	if patchInfo[0] != 0xC6 {
		t.Errorf("patch info %x", patchInfo)
	}
	plain := libre2.DecryptFRAM(sensorUID, patchInfo, fram)
	if plain[4] != 0x03 || int(plain[327])<<8+int(plain[326]) != 21600 || int(plain[24+293])<<8+int(plain[24+292]) != 180 {
		t.Errorf("decrypted FRAM: state %02x, footer %x, minutes at body+292 %x", plain[4], plain[320:344], plain[24+292:24+294])
	}
	if len(resp) != 6 {
		t.Fatalf("enable streaming returned %x", resp)
	}
	if mac := strings.ToUpper(hex.EncodeToString(reverse(resp))); mac != sensor.AdvertisedName() {
		t.Errorf("macAddress %s, want %s", mac, sensor.AdvertisedName())
	}

	// /status agrees.
	r, err := http.Get(srv.URL + "/status")
	if err != nil {
		t.Fatal(err)
	}
	defer r.Body.Close()
	var st map[string]interface{}
	if err := json.NewDecoder(r.Body).Decode(&st); err != nil {
		t.Fatal(err)
	}
	if st["advertisedName"] != sensor.AdvertisedName() || st["streamingEnabled"] != true || st["uid"] != hex.EncodeToString(sensorUID) {
		t.Errorf("status %v", st)
	}
}

func TestNFCErrorsOverHTTP(t *testing.T) {
	srv, _ := newTestServer(t, 0) // not activated
	sensorUID := reverse(nfc(t, srv, map[string]interface{}{"op": "identifier"}))
	patchInfo := nfc(t, srv, map[string]interface{}{"op": "custom", "code": 0xA1, "parameters": ""})
	code, params := libre2.EnableStreamingCommand(sensorUID, patchInfo, 42)

	for _, c := range []struct {
		name   string
		body   map[string]interface{}
		status int
		code   int
	}{
		{"not activated", map[string]interface{}{"op": "custom", "code": int(code), "parameters": hex.EncodeToString(params)}, 422, 0xB0},
		{"wrong d", map[string]interface{}{"op": "custom", "code": int(code), "parameters": hex.EncodeToString(params[:8]) + "00"}, 422, 0x02},
		{"unknown command", map[string]interface{}{"op": "custom", "code": 0xA2, "parameters": ""}, 422, 0x01},
		{"block out of range", map[string]interface{}{"op": "readBlocks", "first": 42, "count": 3}, 422, 0x10},
		{"bad hex", map[string]interface{}{"op": "custom", "code": 0xA1, "parameters": "zz"}, 400, 0},
		{"no code", map[string]interface{}{"op": "custom", "parameters": ""}, 400, 0},
		{"unknown op", map[string]interface{}{"op": "ndef"}, 400, 0},
	} {
		var r errorReply
		status := post(t, srv, c.body, &r)
		if status != c.status || r.Error == "" || r.Code == nil || *r.Code != c.code {
			t.Errorf("%s: HTTP %d %+v; want HTTP %d, code %d", c.name, status, r, c.status, c.code)
		}
	}

	// POST /activate starts it; then the same command succeeds.
	resp, err := http.Post(srv.URL+"/activate", "application/json", nil)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("activate: HTTP %d", resp.StatusCode)
	}
	if mac := nfc(t, srv, map[string]interface{}{"op": "custom", "code": int(code), "parameters": hex.EncodeToString(params)}); len(mac) != 6 {
		t.Errorf("after activation: %x", mac)
	}
	for _, c := range []struct {
		method, path string
		status       int
	}{
		{http.MethodPost, "/activate", http.StatusConflict}, // already activated
		{http.MethodGet, "/nfc", http.StatusMethodNotAllowed},
	} {
		req, _ := http.NewRequest(c.method, srv.URL+c.path, nil)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode != c.status {
			t.Errorf("%s %s: HTTP %d, want %d", c.method, c.path, resp.StatusCode, c.status)
		}
	}
}

func TestLinkControlOverHTTP(t *testing.T) {
	srv, sensor := newTestServer(t, 3*time.Hour)
	id := sensor.NFCIdentifier()
	uid := make([]byte, 8)
	for i := range id {
		uid[i] = id[7-i]
	}
	info, _ := sensor.NFCCustom(0xa1, nil, time.Now())
	code, params := libre2.EnableStreamingCommand(uid, info, 42)
	if _, err := sensor.NFCCustom(code, params, time.Now()); err != nil {
		t.Fatal(err)
	}
	call := func(path, body string) (int, map[string]interface{}) {
		t.Helper()
		resp, err := http.Post(srv.URL+path, "application/json", strings.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		var reply map[string]interface{}
		json.NewDecoder(resp.Body).Decode(&reply)
		return resp.StatusCode, reply
	}
	if s, r := call("/status", ""); s != http.StatusOK || r["advertising"] != true || r["connected"] != false {
		t.Fatalf("status: HTTP %d %v", s, r)
	}
	if s, _ := call("/disconnect", ""); s != http.StatusConflict {
		t.Errorf("disconnect with no link: HTTP %d", s)
	}
	if s, _ := call("/out-of-range", `{"minutes":0}`); s != http.StatusBadRequest {
		t.Errorf("out of range for 0 minutes: HTTP %d", s)
	}
	if s, r := call("/out-of-range", `{"minutes":20}`); s != http.StatusOK || r["advertising"] != false || r["outOfRangeUntil"] == nil {
		t.Errorf("out of range: HTTP %d %v", s, r)
	}
	if s, r := call("/terminate", ""); s != http.StatusOK || r["framState"] != float64(5) || r["terminated"] == nil {
		t.Errorf("terminate: HTTP %d %v", s, r)
	}
}
