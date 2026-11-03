package main

import (
	"encoding/hex"
	"encoding/json"
	"log"
	"net/http"
	"time"

	"libre2sim/libre2"
)

// api exposes the sensor's state and lets a tester steer it:
//
//	GET  /status                 the sensor and its latest reading
//	POST /glucose     {"kind":"fixed","value":65}
//	                  {"kind":"sine","value":120,"amplitude":40,"periodMinutes":240}
//	                  {"kind":"walk","value":140}
//	POST /activate               start a sensor that is still in its applicator
//	                             (LibreTransmitter cannot)
//	POST /disconnect             the sensor drops the link; the app reconnects
//	POST /out-of-range {"minutes":20}
//	                             drop the link and stay unreachable that long
//	POST /terminate              end the sensor now: FRAM state 05, streaming
//	                             stops and the link drops
//	POST /force-quit             the app's end is a force-quit, not a
//	                             termination: drop what it left lodged
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
	mux     *http.ServeMux
}

func newAPI(sensor *libre2.Sensor) *api {
	a := &api{sensor: sensor, mux: http.NewServeMux()}
	a.mux.HandleFunc("/status", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, sensor.Status())
	})
	a.post("/glucose", func(w http.ResponseWriter, r *http.Request) {
		var m libre2.GlucoseMode
		if err := json.NewDecoder(r.Body).Decode(&m); err != nil || m.Kind == "" {
			writeError(w, http.StatusBadRequest, "bad glucose mode", 0)
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
		writeJSON(w, map[string]string{"status": "restarting with a new sensor"})
		go func() {
			time.Sleep(300 * time.Millisecond)
			a.replace(c)
		}()
	})
	a.post("/nfc", a.nfc)
	return a
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
		return
	}
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
