package main

import (
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"libre2sim/libre2"
)

// syntheticFRAM is a valid dump made by libre2sim itself: a sensor worn 10
// hours with a fixed model, its FRAM scanned over /nfc and decrypted. No real
// sensor's data.
func syntheticFRAM(t *testing.T) []byte {
	t.Helper()
	srv, sensor := newTestServer(t, 10*time.Hour)
	sensor.SetGlucoseMode(libre2.GlucoseMode{Kind: "fixed", Value: 133})
	var r struct {
		Blocks []string `json:"blocks"`
	}
	if s := post(t, srv, map[string]interface{}{"op": "readBlocks", "first": 0, "count": 43}, &r); s != http.StatusOK || len(r.Blocks) != 43 {
		t.Fatalf("readBlocks: HTTP %d, %d blocks", s, len(r.Blocks))
	}
	var fram []byte
	for _, b := range r.Blocks {
		fram = append(fram, mustHex(t, b)...)
	}
	st := sensor.Status()
	uid, info := mustHex(t, st["uid"].(string)), mustHex(t, st["patchInfo"].(string))
	return libre2.DecryptFRAM(uid, info, fram)
}

func mustHex(t *testing.T, s string) []byte {
	t.Helper()
	b, err := hex.DecodeString(s)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func newTraceServer(t *testing.T) (*httptest.Server, *libre2.Sensor, string) {
	t.Helper()
	state, err := libre2.NewState(15, time.Hour, 3*time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	sensor := libre2.NewSensor(state, t.TempDir()+"/state.json")
	a := newAPI(sensor)
	dir := filepath.Join(t.TempDir(), "traces")
	a.traces = traceStore{dir: dir}
	srv := httptest.NewServer(a)
	t.Cleanup(srv.Close)
	return srv, sensor, dir
}

func TestTracesOverHTTP(t *testing.T) {
	srv, sensor, dir := newTraceServer(t)
	var list []traceEntry
	if s := get(t, srv, "/traces", &list); s != http.StatusOK || len(list) != 0 {
		t.Fatalf("no traces yet: HTTP %d %v", s, list)
	}

	// Add one: a whole file, with its reference glucose.
	b64 := base64.StdEncoding.EncodeToString(syntheticFRAM(t))
	body, _ := json.Marshal(map[string]string{"name": "synthetic-1", "fram": "// currentbg: 133\n" + b64})
	s, r := call(t, srv, "POST", "/traces", string(body))
	if s != http.StatusOK || r["name"] != "synthetic-1" || r["referenceMgdl"] != float64(133) || r["firstMgdl"] != float64(133) || r["minutes"].(float64) < 470 {
		t.Fatalf("add: HTTP %d %v", s, r)
	}
	if _, err := os.Stat(filepath.Join(dir, "synthetic-1.txt")); err != nil {
		t.Error(err)
	}
	if s := get(t, srv, "/traces", &list); s != http.StatusOK || len(list) != 1 || list[0].Name != "synthetic-1" || list[0].Label != "synthetic-1" || list[0].LastMgdl != 133 {
		t.Errorf("list: HTTP %d %+v", s, list)
	}

	// Invalid ones: 400 with a reason, nothing saved.
	short := base64.StdEncoding.EncodeToString(make([]byte, 100))
	spoiled, _ := base64.StdEncoding.DecodeString(b64)
	spoiled[100] ^= 0xff
	for _, c := range []map[string]string{
		{"name": "x", "fram": short},
		{"name": "x", "fram": base64.StdEncoding.EncodeToString(spoiled)},
		{"name": "x", "fram": "%%%"},
		{"name": "../x", "fram": b64},
		{"name": "", "fram": b64},
	} {
		body, _ := json.Marshal(c)
		if s, r := call(t, srv, "POST", "/traces", string(body)); s != http.StatusBadRequest || r["error"] == nil {
			t.Errorf("%v: HTTP %d %v", c, s, r)
		}
	}
	if s, _ := call(t, srv, "POST", "/traces", "{"); s != http.StatusBadRequest {
		t.Errorf("bad JSON: HTTP %d", s)
	}
	if get(t, srv, "/traces", &list); len(list) != 1 {
		t.Errorf("after the invalid ones: %+v", list)
	}

	// Select it, and an unknown one.
	s, r = call(t, srv, "POST", "/glucose", `{"kind":"trace","trace":"synthetic-1"}`)
	if m, _ := r["glucoseMode"].(map[string]interface{}); s != http.StatusOK || m["kind"] != "trace" || m["trace"] != "synthetic-1" {
		t.Fatalf("select: HTTP %d %v", s, r)
	}
	if n := sensor.Status()["nextReading"].(*libre2.NextReading); n.MgDl != 133 || n.Estimate {
		t.Errorf("next %+v", n)
	}
	for _, c := range []string{`{"kind":"trace","trace":"nope"}`, `{"kind":"trace"}`, `{"kind":"trace","trace":"../etc"}`} {
		if s, r := call(t, srv, "POST", "/glucose", c); s != http.StatusBadRequest || r["error"] == nil {
			t.Errorf("%s: HTTP %d %v", c, s, r)
		}
	}
	if s, _ := call(t, srv, "DELETE", "/traces", ""); s != http.StatusMethodNotAllowed {
		t.Errorf("DELETE: HTTP %d", s)
	}
}

// minutes=all is the whole wear so far; step thins it and drops the records.
func TestReadingsStepOverHTTP(t *testing.T) {
	srv, _ := newTestServer(t, 10*time.Hour)
	var h map[string]interface{}
	get(t, srv, "/readings?minutes=all&step=30", &h)
	if h["step"] != float64(30) || h["firstMinute"] != float64(0) || len(h["mgdl"].([]interface{})) != 21 || h["records"] != nil {
		t.Errorf("step 30: %v", h)
	}
	get(t, srv, "/readings?minutes=all", &h)
	if h["step"] != float64(1) || len(h["mgdl"].([]interface{})) != 601 || len(h["records"].([]interface{})) != 601 {
		t.Errorf("all: step %v, %d values", h["step"], len(h["mgdl"].([]interface{})))
	}
}
