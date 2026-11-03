package main

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
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

// call posts body to path and decodes the JSON reply.
func call(t *testing.T, srv *httptest.Server, method, path, body string) (int, map[string]interface{}) {
	t.Helper()
	req, err := http.NewRequest(method, srv.URL+path, strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var reply map[string]interface{}
	json.NewDecoder(resp.Body).Decode(&reply)
	return resp.StatusCode, reply
}

func TestControlPanel(t *testing.T) {
	srv, _ := newTestServer(t, 3*time.Hour)
	resp, err := http.Get(srv.URL + "/")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var b bytes.Buffer
	b.ReadFrom(resp.Body)
	html := b.String()
	if resp.StatusCode != http.StatusOK || !strings.HasPrefix(resp.Header.Get("Content-Type"), "text/html") {
		t.Fatalf("GET /: HTTP %d, %s", resp.StatusCode, resp.Header.Get("Content-Type"))
	}
	for _, want := range []string{"libre2sim", "/status", "/glucose", "/disconnect", "/out-of-range", "/terminate", "/fault", "/activate", "/new"} {
		if !strings.Contains(html, want) {
			t.Errorf("the panel does not mention %s", want)
		}
	}
	// Self-contained: nothing is loaded from elsewhere.
	for _, bad := range []string{"src=\"http", "href=\"http", "src='http", "@import", "//cdn"} {
		if strings.Contains(html, bad) {
			t.Errorf("the panel loads an external asset: %s", bad)
		}
	}
	if r, err := http.Get(srv.URL + "/nope"); err != nil || r.StatusCode != http.StatusNotFound {
		t.Errorf("GET /nope: %v, %v", r, err)
	}
}

func TestGlucoseOverHTTP(t *testing.T) {
	srv, sensor := newTestServer(t, 3*time.Hour)
	if s, r := call(t, srv, "POST", "/glucose", `{"kind":"fixed","value":65}`); s != http.StatusOK || r["glucoseMode"] == nil {
		t.Fatalf("fixed 65: HTTP %d %v", s, r)
	}
	if got := sensor.Status()["glucoseMode"].(libre2.GlucoseMode); got.Kind != "fixed" || got.Value != 65 {
		t.Errorf("mode %+v", got)
	}
	for _, c := range []string{`{"kind":"sine","value":120,"amplitude":40,"periodMinutes":240}`, `{"kind":"walk","value":140}`} {
		if s, _ := call(t, srv, "POST", "/glucose", c); s != http.StatusOK {
			t.Errorf("%s: HTTP %d", c, s)
		}
	}
	for _, c := range []string{``, `{`, `{}`, `{"kind":"square","value":1}`, `[1]`} {
		if s, r := call(t, srv, "POST", "/glucose", c); s != http.StatusBadRequest || r["error"] == nil {
			t.Errorf("%q: HTTP %d %v, want 400", c, s, r)
		}
	}
	if s, _ := call(t, srv, "GET", "/glucose", ""); s != http.StatusMethodNotAllowed {
		t.Errorf("GET /glucose: HTTP %d", s)
	}
}

func TestFaultOverHTTP(t *testing.T) {
	srv, sensor := newTestServer(t, 3*time.Hour)
	if _, r := call(t, srv, "GET", "/status", ""); r["fault"] != nil {
		t.Fatalf("a fault is armed at the start: %v", r["fault"])
	}
	for _, k := range []string{"badcrc", "short"} {
		if s, r := call(t, srv, "POST", "/fault", `{"kind":"`+k+`"}`); s != http.StatusOK || r["fault"] != k {
			t.Fatalf("arm %s: HTTP %d %v", k, s, r)
		}
		if _, r := call(t, srv, "GET", "/status", ""); r["fault"] != k {
			t.Errorf("status fault %v, want %s", r["fault"], k)
		}
		if sensor.Fault() != k {
			t.Errorf("sensor fault %q, want %s", sensor.Fault(), k)
		}
	}
	if s, r := call(t, srv, "POST", "/fault", `{"kind":"none"}`); s != http.StatusOK || r["fault"] != nil || sensor.Fault() != "" {
		t.Errorf("disarm: HTTP %d %v, fault %q", s, r, sensor.Fault())
	}
	sensor.ArmFault("short")
	for _, c := range []string{``, `{}`, `{"kind":"melt"}`, `{"kind":3}`} {
		if s, r := call(t, srv, "POST", "/fault", c); s != http.StatusBadRequest || r["error"] == nil {
			t.Errorf("%q: HTTP %d %v, want 400", c, s, r)
		}
	}
	if sensor.Fault() != "short" {
		t.Errorf("a refused fault changed the armed one to %q", sensor.Fault())
	}
	if s, _ := call(t, srv, "GET", "/fault", ""); s != http.StatusMethodNotAllowed {
		t.Errorf("GET /fault: HTTP %d", s)
	}
}

func TestActivateAndForceQuitOverHTTP(t *testing.T) {
	srv, _ := newTestServer(t, 0) // in its applicator
	if _, r := call(t, srv, "GET", "/status", ""); r["activated"] != nil {
		t.Fatalf("activated at the start: %v", r["activated"])
	}
	if s, r := call(t, srv, "POST", "/activate", ""); s != http.StatusOK || r["activated"] == nil {
		t.Errorf("activate: HTTP %d %v", s, r)
	}
	if s, r := call(t, srv, "POST", "/activate", ""); s != http.StatusConflict || r["error"] == nil {
		t.Errorf("activate again: HTTP %d %v", s, r)
	}
	if s, _ := call(t, srv, "POST", "/force-quit", ""); s != http.StatusNoContent {
		t.Errorf("force-quit: HTTP %d", s)
	}
	if s, _ := call(t, srv, "GET", "/force-quit", ""); s != http.StatusMethodNotAllowed {
		t.Errorf("GET /force-quit: HTTP %d", s)
	}
}

func TestNewOverHTTP(t *testing.T) {
	srv, _ := newTestServer(t, 3*time.Hour)
	// Without a way to restart, there is no /new.
	if s, _ := call(t, srv, "POST", "/new", `{}`); s != http.StatusNotImplemented {
		t.Errorf("/new without replace: HTTP %d", s)
	}

	state, err := libre2.NewState(15, time.Hour, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	a := newAPI(libre2.NewSensor(state, t.TempDir()+"/state.json"))
	got := make(chan sensorConfig, 1)
	a.replace = func(c sensorConfig) { got <- c }
	srv2 := httptest.NewServer(a)
	defer srv2.Close()
	for _, c := range []string{`{`, `{"wearDays":-1}`, `{"warmupMinutes":-5}`} {
		if s, r := call(t, srv2, "POST", "/new", c); s != http.StatusBadRequest || r["error"] == nil {
			t.Errorf("%q: HTTP %d %v, want 400", c, s, r)
		}
	}
	if s, r := call(t, srv2, "POST", "/new", `{"wearDays":10,"warmupMinutes":5,"ageMinutes":30}`); s != http.StatusOK || r["status"] == nil {
		t.Fatalf("/new: HTTP %d %v", s, r)
	}
	select {
	case c := <-got:
		if c.WearDays != 10 || *c.WarmupMinutes != 5 || c.AgeMinutes != 30 {
			t.Errorf("config %+v", c)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("/new never replaced the sensor")
	}
}

// Every control endpoint takes POST only (/status and / take GET).
func TestMethodsOverHTTP(t *testing.T) {
	srv, _ := newTestServer(t, 3*time.Hour)
	for _, p := range []string{"/glucose", "/activate", "/disconnect", "/out-of-range", "/terminate", "/fault", "/force-quit", "/new", "/nfc"} {
		if s, r := call(t, srv, "GET", p, ""); s != http.StatusMethodNotAllowed || r["error"] == nil {
			t.Errorf("GET %s: HTTP %d %v", p, s, r)
		}
	}
	if s, r := call(t, srv, "POST", "/out-of-range", `{"minutes":"x"}`); s != http.StatusBadRequest || r["error"] == nil {
		t.Errorf("out-of-range with a string: HTTP %d %v", s, r)
	}
	if s, _ := call(t, srv, "POST", "/out-of-range", `{"minutes":-3}`); s != http.StatusBadRequest {
		t.Errorf("out-of-range for -3 minutes: HTTP %d", s)
	}
	if s, _ := call(t, srv, "POST", "/nfc", `{`); s != http.StatusBadRequest {
		t.Errorf("/nfc with bad JSON: HTTP %d", s)
	}
}

func get(t *testing.T, srv *httptest.Server, path string, reply interface{}) int {
	t.Helper()
	resp, err := http.Get(srv.URL + path)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if err := json.NewDecoder(resp.Body).Decode(reply); err != nil {
		t.Fatalf("GET %s: %v", path, err)
	}
	return resp.StatusCode
}

type eventsReply struct {
	Last   uint64         `json:"last"`
	Events []libre2.Event `json:"events"`
}

// Every /nfc request and its answer, an NFC error included, is logged, and
// /events?since= returns only what is new.
func TestEventsOverHTTP(t *testing.T) {
	srv, sensor := newTestServer(t, 3*time.Hour)
	nfc(t, srv, map[string]interface{}{"op": "identifier"})
	var blocks struct {
		Blocks []string `json:"blocks"`
	}
	if s := post(t, srv, map[string]interface{}{"op": "readBlocks", "first": 0, "count": 3}, &blocks); s != http.StatusOK || len(blocks.Blocks) != 3 {
		t.Fatalf("readBlocks: HTTP %d, %v", s, blocks)
	}
	var e errorReply
	if s := post(t, srv, map[string]interface{}{"op": "readBlocks", "first": 40, "count": 9}, &e); s != http.StatusUnprocessableEntity {
		t.Fatalf("HTTP %d", s)
	}
	call(t, srv, "POST", "/glucose", `{"kind":"fixed","value":99}`)
	call(t, srv, "POST", "/fault", `{"kind":"short"}`)

	var r eventsReply
	if s := get(t, srv, "/events", &r); s != http.StatusOK {
		t.Fatalf("GET /events: HTTP %d", s)
	}
	var msgs []string
	for i, ev := range r.Events {
		if ev.Seq != uint64(i+1) || ev.Time.IsZero() {
			t.Errorf("event %d: %+v", i, ev)
		}
		msgs = append(msgs, ev.Cat+"/"+ev.Kind+": "+ev.Msg)
	}
	all := strings.Join(msgs, "\n")
	id := hex.EncodeToString(sensor.NFCIdentifier())
	for _, want := range []string{
		"nfc/request: identifier", "nfc/response: ok: " + id,
		"nfc/request: readBlocks first=0 count=3", "nfc/response: 3 blocks: ",
		"nfc/response: HTTP 422, NFC error code 16 (0x10)",
		"sensor/glucose-mode: glucose mode now fixed 99", "sensor/fault-armed: fault short armed",
	} {
		if !strings.Contains(all, want) {
			t.Errorf("no event %q in\n%s", want, all)
		}
	}
	if r.Last != uint64(len(r.Events)) {
		t.Errorf("last %d for %d events", r.Last, len(r.Events))
	}
	var r2 eventsReply
	get(t, srv, "/events?since="+strconv.FormatUint(r.Last-1, 10), &r2)
	if len(r2.Events) != 1 || r2.Events[0].Seq != r.Last {
		t.Errorf("since last-1: %+v", r2)
	}
	get(t, srv, "/events?since="+strconv.FormatUint(r.Last, 10), &r2)
	if r2.Events == nil || len(r2.Events) != 0 {
		t.Errorf("since last: %+v, want an empty list", r2.Events)
	}
}

func TestNFCCustomEventsOverHTTP(t *testing.T) {
	srv, sensor := newTestServer(t, 3*time.Hour)
	uid := reverse(nfc(t, srv, map[string]interface{}{"op": "identifier"}))
	info := nfc(t, srv, map[string]interface{}{"op": "custom", "code": 0xa1, "parameters": ""})
	code, params := libre2.EnableStreamingCommand(uid, info, 424242)
	nfc(t, srv, map[string]interface{}{"op": "custom", "code": int(code), "parameters": hex.EncodeToString(params)})
	ev, _ := sensor.Events().Since(0)
	var enabled bool
	for _, e := range ev {
		if e.Cat == libre2.CatNFC && e.Kind == "streaming-enabled" && strings.Contains(e.Msg, "424242") {
			enabled = true
		}
	}
	if !enabled {
		t.Errorf("no streaming-enabled event: %+v", ev)
	}
}

func TestReadingsOverHTTP(t *testing.T) {
	srv, _ := newTestServer(t, 3*time.Hour)
	call(t, srv, "POST", "/glucose", `{"kind":"fixed","value":110}`)
	var h libre2.History
	if s := get(t, srv, "/readings?minutes=30", &h); s != http.StatusOK {
		t.Fatalf("HTTP %d", s)
	}
	if len(h.Values) != 30 || h.Next == nil || h.Trend == nil || h.Next.At == "" {
		t.Fatalf("readings: %+v", h)
	}
	// /status gains the next reading and the trend; its other fields stay.
	var st map[string]interface{}
	get(t, srv, "/status", &st)
	next, _ := st["nextReading"].(map[string]interface{})
	if next == nil || next["inSeconds"] == nil || st["trend"] == nil || st["glucose"] == nil || st["uid"] == nil || st["lifeCount"] == nil {
		t.Errorf("status: %v", st)
	}
	// A bad or missing minutes falls back to 60.
	get(t, srv, "/readings?minutes=x", &h)
	if len(h.Values) != 60 {
		t.Errorf("default: %d values", len(h.Values))
	}
}

func TestConfigOverHTTP(t *testing.T) {
	state, err := libre2.NewState(15, time.Hour, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	sensor := libre2.NewSensor(state, t.TempDir()+"/state.json")
	a := newAPI(sensor)
	a.launch = launchConfig{Version: "abc123", StatePath: "s.json", API: ":8081", Socket: "/tmp/x.sock", Days: 15, WarmupMinutes: 60}
	srv := httptest.NewServer(a)
	defer srv.Close()

	var c struct {
		Launch       launchConfig           `json:"launch"`
		StrictUnlock bool                   `json:"strictUnlock"`
		Sensor       map[string]interface{} `json:"sensor"`
	}
	if s := get(t, srv, "/config", &c); s != http.StatusOK {
		t.Fatalf("HTTP %d", s)
	}
	if c.Launch != a.launch || c.StrictUnlock || c.Sensor["serial"] != sensor.Serial() || c.Sensor["uid"] == nil ||
		c.Sensor["bleAddress"] == nil || c.Sensor["calibration"] == nil || c.Sensor["patchInfo"] == nil {
		t.Errorf("config: %+v", c)
	}

	if s, r := call(t, srv, "POST", "/config", `{"strictUnlock":true}`); s != http.StatusOK || r["strictUnlock"] != true {
		t.Errorf("POST /config: HTTP %d %v", s, r)
	}
	if !sensor.StrictUnlock() {
		t.Error("strict unlock not set")
	}
	var st map[string]interface{}
	get(t, srv, "/status", &st)
	if st["strictUnlock"] != true {
		t.Errorf("status strictUnlock %v", st["strictUnlock"])
	}
	call(t, srv, "POST", "/config", `{"strictUnlock":false}`)
	if sensor.StrictUnlock() {
		t.Error("strict unlock not cleared")
	}
	for _, body := range []string{`{`, `{}`, `{"strictUnlock":"yes"}`} {
		if s, r := call(t, srv, "POST", "/config", body); s != http.StatusBadRequest || r["error"] == nil {
			t.Errorf("%q: HTTP %d %v", body, s, r)
		}
	}
	if s, _ := call(t, srv, "PUT", "/config", `{}`); s != http.StatusMethodNotAllowed {
		t.Errorf("PUT /config: HTTP %d", s)
	}
	var ev eventsReply
	get(t, srv, "/events", &ev)
	if len(ev.Events) != 2 || ev.Events[0].Kind != "config" {
		t.Errorf("events %+v", ev.Events)
	}
}

func TestEndWarmupOverHTTP(t *testing.T) {
	srv, _ := newTestServer(t, 10*time.Minute)
	if s, r := call(t, srv, "POST", "/end-warmup", ""); s != http.StatusOK || r["framState"] == nil {
		t.Fatalf("in warmup: HTTP %d %v", s, r)
	}
	if s, _ := call(t, srv, "POST", "/end-warmup", ""); s != http.StatusConflict {
		t.Errorf("again: HTTP %d, want 409", s)
	}
	if s, _ := call(t, srv, "GET", "/end-warmup", ""); s != http.StatusMethodNotAllowed {
		t.Errorf("GET: HTTP %d, want 405", s)
	}
}
