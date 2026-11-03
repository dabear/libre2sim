package main

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"strconv"
	"strings"
	"time"

	"libre2sim/libre2"
)

// api is libre2sim's control API. The whole of it, as libre3sim's, with the
// Libre 2's own additions (/fault, /nfc, /activate). A POST that needs a body
// answers 400 {"error","code"} to one it cannot use; a request for something
// that is not possible now answers 409. Every POST except /new, /force-quit
// and /nfc answers with the sensor's /status.
//
//	GET  /                       the control panel (web.go)
//	GET  /status                 the sensor and its latest reading; "fault" is
//	                             there while a fault is armed
//	GET  /readings?minutes=60    the latest readings (mg/dL by minute, oldest
//	                             first) with the next reading and the trend
//	GET  /events?since=<seq>     the event log, oldest first: {"last":seq,
//	                             "events":[{seq,time,cat,kind,msg}]}
//	GET  /config                 the launch configuration and the sensor's identity
//	POST /config      {"strictUnlock":true}
//	                             change a setting that is safe at run time
//	POST /glucose     {"kind":"fixed","value":65}
//	                  {"kind":"sine","value":120,"amplitude":40,"periodMinutes":240}
//	                  {"kind":"walk","value":140}
//	                             how readings are made up from the next minute on;
//	                             400 on any other kind
//	POST /activate               start a sensor that is still in its applicator
//	POST /end-warmup             end the warmup at the next minute; 409 unless in warmup
//	                             (LibreTransmitter cannot); 409 if already started
//	POST /disconnect             the sensor drops the link; the app reconnects;
//	                             409 if no phone is connected
//	POST /out-of-range {"minutes":20}
//	                             drop the link and stay unreachable that long;
//	                             400 unless minutes > 0
//	POST /terminate              end the sensor now: FRAM state 05, streaming
//	                             stops and the link drops
//	POST /fault {"kind":"badcrc"|"short"|"none"}
//	                             spoil the NEXT packet only: badcrc inverts its
//	                             plaintext CRC (checksumValidationError in
//	                             LibreTransmitter), short sends 45 bytes; none
//	                             disarms. 400 on any other kind
//	POST /force-quit             the app's end is a force-quit, not a
//	                             termination: drop what it left lodged (204)
//	POST /new         {"wearDays":15,"warmupMinutes":60,"ageMinutes":0}
//	                             replace the sensor with a new one and restart;
//	                             ageMinutes 0 is a sensor still in its applicator
//
// and what an app in the Simulator needs in place of the sensor's NFC tag
// (LibreTransmitter's SimulatedSensorPairingService):
//
//	POST /nfc  {"op":"identifier"}                    -> {"response":"<hex>"}  tag.identifier
//	           {"op":"systemInfo"}                    -> {"response":""}       getSystemInfo
//	           {"op":"readBlocks","first":n,"count":n} -> {"blocks":["<hex>",…]} readMultipleBlocks
//	           {"op":"custom","code":161,"parameters":"<hex>"}
//	                                                  -> {"response":"<hex>"}  customCommand
//	           an NFC error is HTTP 422 {"error":"…","code":n}
type api struct {
	sensor *libre2.Sensor
	// radio is the BLE transport; nil (in tests) means no link to drop.
	radio *SimRadio
	// replace swaps in a new sensor; nil disables POST /new.
	replace func(sensorConfig)
	// launch is how libre2sim was started, for GET /config.
	launch launchConfig
	mux    *http.ServeMux
}

// launchConfig is how libre2sim was launched (the flags), shown read-only by
// GET /config. strictUnlock is the exception, read from the sensor: it can
// change at run time.
type launchConfig struct {
	Version       string  `json:"version"`
	StatePath     string  `json:"statePath"`
	API           string  `json:"api"`
	Socket        string  `json:"socket"`
	Fresh         bool    `json:"fresh"`
	Days          int     `json:"days"`
	WarmupMinutes float64 `json:"warmupMinutes"`
	AgeMinutes    float64 `json:"ageMinutes"`
	Simulator     string  `json:"simulator"`
	Relaunch      bool    `json:"relaunch"`
	Suspend       bool    `json:"suspend"`
	Dylib         string  `json:"dylib"`
}

func newAPI(sensor *libre2.Sensor) *api {
	a := &api{sensor: sensor, mux: http.NewServeMux()}
	a.mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		io.WriteString(w, indexHTML)
	})
	a.mux.HandleFunc("/status", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, sensor.Status())
	})
	a.mux.HandleFunc("/readings", func(w http.ResponseWriter, r *http.Request) {
		minutes, err := strconv.Atoi(r.URL.Query().Get("minutes"))
		if err != nil || minutes < 1 {
			minutes = 60
		}
		if minutes > 1440 {
			minutes = 1440
		}
		writeJSON(w, sensor.History(minutes, time.Now()))
	})
	a.mux.HandleFunc("/events", func(w http.ResponseWriter, r *http.Request) {
		since, _ := strconv.ParseUint(r.URL.Query().Get("since"), 10, 64)
		events, last := sensor.Events().Since(since)
		writeJSON(w, map[string]interface{}{"last": last, "events": events})
	})
	a.mux.HandleFunc("/config", func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
		case http.MethodPost:
			var req struct {
				StrictUnlock *bool `json:"strictUnlock"`
			}
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.StrictUnlock == nil {
				writeError(w, http.StatusBadRequest, `want {"strictUnlock":true|false}`, 0)
				return
			}
			sensor.SetStrictUnlock(*req.StrictUnlock)
			log.Printf("strict unlock now %v", *req.StrictUnlock)
			sensor.Events().Add(libre2.CatSensor, "config", "strict unlock now %v", *req.StrictUnlock)
		default:
			writeError(w, http.StatusMethodNotAllowed, "GET or POST /config", 0)
			return
		}
		writeJSON(w, a.config())
	})
	a.post("/glucose", func(w http.ResponseWriter, r *http.Request) {
		var m libre2.GlucoseMode
		if err := json.NewDecoder(r.Body).Decode(&m); err != nil || (m.Kind != "fixed" && m.Kind != "sine" && m.Kind != "walk") {
			writeError(w, http.StatusBadRequest, `bad glucose mode; want kind "fixed", "sine" or "walk"`, 0)
			return
		}
		sensor.SetGlucoseMode(m)
		log.Printf("glucose mode now %s", m)
		writeJSON(w, sensor.Status())
	})
	a.post("/activate", func(w http.ResponseWriter, r *http.Request) {
		if err := sensor.Activate(time.Now()); err != nil {
			writeError(w, http.StatusConflict, err.Error(), 0)
			return
		}
		log.Printf("activated")
		writeJSON(w, sensor.Status())
	})
	a.post("/end-warmup", func(w http.ResponseWriter, r *http.Request) {
		if err := sensor.EndWarmup(time.Now()); err != nil {
			writeError(w, http.StatusConflict, err.Error(), 0)
			return
		}
		log.Printf("warmup ended early")
		writeJSON(w, sensor.Status())
	})
	a.post("/disconnect", func(w http.ResponseWriter, r *http.Request) {
		if a.radio == nil || !a.radio.Disconnect() {
			writeError(w, http.StatusConflict, "no phone is connected", 0)
			return
		}
		log.Printf("dropped the link")
		writeJSON(w, sensor.Status())
	})
	a.post("/out-of-range", func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Minutes float64 `json:"minutes"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Minutes <= 0 {
			writeError(w, http.StatusBadRequest, `want {"minutes":n}`, 0)
			return
		}
		sensor.GoOutOfRange(time.Duration(req.Minutes * float64(time.Minute)))
		if a.radio != nil {
			a.radio.Disconnect()
		}
		log.Printf("out of range for %g minutes", req.Minutes)
		writeJSON(w, sensor.Status())
	})
	a.post("/terminate", func(w http.ResponseWriter, r *http.Request) {
		// The link's handler sees the sensor has ended within a second and
		// hangs up; the sensor no longer advertises.
		sensor.Terminate(time.Now())
		log.Printf("terminated")
		writeJSON(w, sensor.Status())
	})
	a.post("/fault", func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Kind string `json:"kind"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Kind == "" {
			writeError(w, http.StatusBadRequest, `want {"kind":"badcrc"|"short"|"none"}`, 0)
			return
		}
		if err := sensor.ArmFault(req.Kind); err != nil {
			writeError(w, http.StatusBadRequest, err.Error(), 0)
			return
		}
		if req.Kind == libre2.FaultNone {
			log.Printf("fault disarmed")
		} else {
			log.Printf("fault %s armed for the next packet", req.Kind)
		}
		writeJSON(w, sensor.Status())
	})
	a.post("/force-quit", func(w http.ResponseWriter, r *http.Request) {
		if a.radio != nil {
			a.radio.ForceQuit()
		}
		w.WriteHeader(http.StatusNoContent)
	})
	a.post("/new", func(w http.ResponseWriter, r *http.Request) {
		if a.replace == nil {
			writeError(w, http.StatusNotImplemented, "replacing the sensor is not available", 0)
			return
		}
		var c sensorConfig
		if err := json.NewDecoder(r.Body).Decode(&c); err != nil {
			writeError(w, http.StatusBadRequest, "bad sensor config: "+err.Error(), 0)
			return
		}
		if _, err := c.state(); err != nil {
			writeError(w, http.StatusBadRequest, err.Error(), 0)
			return
		}
		sensor.Events().Add(libre2.CatSensor, "new-sensor", "new sensor requested (wearDays %d, age %g min); libre2sim restarts and this log starts over", c.WearDays, c.AgeMinutes)
		writeJSON(w, map[string]string{"status": "restarting with a new sensor"})
		go func() {
			time.Sleep(300 * time.Millisecond)
			a.replace(c)
		}()
	})
	a.post("/nfc", a.nfc)
	return a
}

// config is GET /config: {"launch":{…},"sensor":{…}}.
func (a *api) config() map[string]interface{} {
	st := a.sensor.Status()
	sensor := map[string]interface{}{}
	for _, k := range []string{"uid", "serial", "patchInfo", "bleAddress", "advertisedName", "calibration", "temperature",
		"wearMinutes", "warmupMinutes", "activated", "streamingCode"} {
		if v, ok := st[k]; ok {
			sensor[k] = v
		}
	}
	launch := a.launch
	if launch.Version == "" {
		launch.Version = "dev"
	}
	return map[string]interface{}{
		"launch":       launch,
		"strictUnlock": a.sensor.StrictUnlock(),
		"sensor":       sensor,
	}
}

func (a *api) ServeHTTP(w http.ResponseWriter, r *http.Request) { a.mux.ServeHTTP(w, r) }

// nfcRequest is one CoreNFC call, as SimulatedSensorPairingService sends it.
type nfcRequest struct {
	Op         string `json:"op"`
	First      int    `json:"first"`
	Count      int    `json:"count"`
	Code       *int   `json:"code"`
	Parameters string `json:"parameters"`
}

func (a *api) nfc(w http.ResponseWriter, r *http.Request) {
	var req nfcRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "bad NFC request: "+err.Error(), 0)
		a.sensor.Events().Add(libre2.CatNFC, "bad-request", "bad NFC request: %v", err)
		return
	}
	ev := a.sensor.Events()
	ev.Add(libre2.CatNFC, "request", "%s", req.describe())
	rec := &recorder{ResponseWriter: w, status: http.StatusOK}
	a.nfcOp(rec, req)
	ev.Add(libre2.CatNFC, "response", "%s", rec.describe())
}

// describe is the request as the event log shows it.
func (req nfcRequest) describe() string {
	switch req.Op {
	case "readBlocks":
		return fmt.Sprintf("readBlocks first=%d count=%d", req.First, req.Count)
	case "custom":
		if req.Code != nil {
			return fmt.Sprintf("custom code=0x%02x parameters=%s", *req.Code, req.Parameters)
		}
	}
	return req.Op
}

// recorder keeps what a handler answers, for the event log.
type recorder struct {
	http.ResponseWriter
	status int
	body   bytes.Buffer
}

func (r *recorder) WriteHeader(status int) {
	r.status = status
	r.ResponseWriter.WriteHeader(status)
}

func (r *recorder) Write(b []byte) (int, error) {
	r.body.Write(b)
	return r.ResponseWriter.Write(b)
}

// describe is the NFC answer as the event log shows it: the hex, or the
// error and its code.
func (r *recorder) describe() string {
	var m struct {
		Response *string  `json:"response"`
		Blocks   []string `json:"blocks"`
		Error    string   `json:"error"`
		Code     int      `json:"code"`
	}
	json.Unmarshal(r.body.Bytes(), &m)
	switch {
	case r.status != http.StatusOK:
		return fmt.Sprintf("HTTP %d, NFC error code %d (0x%02x): %s", r.status, m.Code, m.Code, m.Error)
	case m.Blocks != nil:
		return fmt.Sprintf("%d blocks: %s", len(m.Blocks), strings.Join(m.Blocks, ""))
	case m.Response != nil && *m.Response == "":
		return "ok, empty response"
	case m.Response != nil:
		return "ok: " + *m.Response
	}
	return "ok"
}

func (a *api) nfcOp(w http.ResponseWriter, req nfcRequest) {
	switch req.Op {
	case "identifier":
		id := a.sensor.NFCIdentifier()
		log.Printf("NFC identifier -> %x", id)
		writeJSON(w, map[string]string{"response": hex.EncodeToString(id)})
	case "systemInfo":
		// LibreTransmitter only needs getSystemInfo to succeed.
		writeJSON(w, map[string]string{"response": ""})
	case "readBlocks":
		blocks, err := a.sensor.NFCReadBlocks(req.First, req.Count)
		if err != nil {
			writeNFCError(w, "readBlocks", err)
			return
		}
		out := make([]string, len(blocks))
		for i, b := range blocks {
			out[i] = hex.EncodeToString(b)
		}
		if req.First == 0 {
			log.Printf("NFC reading FRAM")
		}
		writeJSON(w, map[string][]string{"blocks": out})
	case "custom":
		if req.Code == nil || *req.Code < 0 || *req.Code > 0xff {
			writeError(w, http.StatusBadRequest, `custom: want "code" 0 to 255`, 0)
			return
		}
		params, err := hex.DecodeString(req.Parameters)
		if err != nil {
			writeError(w, http.StatusBadRequest, "custom: parameters: want hex", 0)
			return
		}
		code := byte(*req.Code)
		resp, err := a.sensor.NFCCustom(code, params, time.Now())
		if err != nil {
			writeNFCError(w, "custom", err)
			return
		}
		log.Printf("NFC %02x %x -> %x", code, params, resp)
		writeJSON(w, map[string]string{"response": hex.EncodeToString(resp)})
	default:
		writeError(w, http.StatusBadRequest, `unknown op; want "identifier", "systemInfo", "readBlocks" or "custom"`, 0)
	}
}

func writeNFCError(w http.ResponseWriter, op string, err error) {
	log.Printf("NFC %s: %v", op, err)
	code := 0
	if e, ok := err.(*libre2.NFCError); ok {
		code = int(e.Code)
	}
	writeError(w, http.StatusUnprocessableEntity, err.Error(), code)
}

func (a *api) post(path string, f http.HandlerFunc) {
	a.mux.HandleFunc(path, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			writeError(w, http.StatusMethodNotAllowed, "POST to "+path, 0)
			return
		}
		f(w, r)
	})
}

// writeError answers {"error":msg,"code":code}, which
// SimulatedSensorPairingService shows for any status other than 200.
func writeError(w http.ResponseWriter, status int, msg string, code int) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(map[string]interface{}{"error": msg, "code": code})
}

func writeJSON(w http.ResponseWriter, v interface{}) {
	w.Header().Set("Content-Type", "application/json")
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	enc.Encode(v)
}
