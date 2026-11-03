// Command libre2sim emulates a FreeStyle Libre 2 Plus (EU) sensor for
// LibreTransmitter. For development use only: not an Abbott product, and its
// data is made up.
package main

import (
	"flag"
	"log"
	"net/http"
	"os"
	"syscall"
	"time"

	"libre2sim/libre2"
)

// sensorConfig describes a new sensor. Zero values pick defaults.
type sensorConfig struct {
	WearDays      int      `json:"wearDays"`
	WarmupMinutes *float64 `json:"warmupMinutes"`
	AgeMinutes    float64  `json:"ageMinutes"`
}

func (c sensorConfig) state() (*libre2.State, error) {
	if c.WearDays == 0 {
		c.WearDays = 15
	}
	warmup := time.Hour
	if c.WarmupMinutes != nil {
		warmup = time.Duration(*c.WarmupMinutes * float64(time.Minute))
	}
	return libre2.NewState(c.WearDays, warmup, time.Duration(c.AgeMinutes*float64(time.Minute)))
}

func main() {
	statePath := flag.String("state", "libre2state.json", "sensor state file")
	fresh := flag.Bool("fresh", false, "start a new sensor instead of restoring the state file")
	age := flag.Duration("age", 0, "how long ago a new sensor was activated; 0 leaves it in its applicator (POST /activate starts it)")
	days := flag.Int("days", 15, "wear duration of a new sensor, in days")
	warmup := flag.Duration("warmup", time.Hour, "warmup of a new sensor")
	apiAddr := flag.String("api", ":8081", "HTTP control and NFC API address")
	flag.Parse()
	log.SetFlags(log.Ltime | log.Lmicroseconds)

	var state *libre2.State
	if !*fresh {
		s, err := libre2.LoadState(*statePath)
		if err != nil && !os.IsNotExist(err) {
			log.Fatalf("restoring %s: %v (use -fresh to start a new sensor)", *statePath, err)
		}
		state = s
	}
	if state == nil {
		c := sensorConfig{WearDays: *days, AgeMinutes: age.Minutes()}
		m := warmup.Minutes()
		c.WarmupMinutes = &m
		s, err := c.state()
		if err != nil {
			log.Fatal(err)
		}
		if err := s.Save(*statePath); err != nil {
			log.Fatalf("saving %s: %v", *statePath, err)
		}
		state = s
		log.Printf("new sensor")
	}

	log.Printf("libre2sim — for development use only, not an Abbott product; simulated data only")
	activated := "not yet activated (POST /activate to start it)"
	if state.Activated != nil {
		activated = "activated " + state.Activated.Format(time.RFC3339)
	}
	log.Printf("serial %s, UID %s, patch info %s, BLE address %s, %d-day wear, %d-minute warmup, %s",
		state.Serial(), state.UID, state.PatchInfo, state.BLEAddress, state.WearMinutes/1440, state.WarmupMinutes, activated)

	sensor := libre2.NewSensor(state, *statePath)

	// TODO(M4): start the BLE radio here (ImpossiBLE transport, advertising
	// AdvertisedName once streaming is enabled, F001 unlock, F002 packets),
	// and stop it in replace before re-executing.

	// replace swaps in a new sensor: it is saved over the state file and the
	// emulator re-executes itself, so the new sensor starts exactly as a
	// normal launch does.
	replace := func(c sensorConfig) {
		next, err := c.state()
		if err != nil {
			log.Printf("new sensor: %v", err)
			return
		}
		sensor.Retire()
		if err := next.Save(*statePath); err != nil {
			log.Fatalf("saving %s: %v", *statePath, err)
		}
		exe, err := os.Executable()
		if err != nil {
			log.Fatalf("restarting: %v", err)
		}
		log.Printf("restarting as the new sensor")
		args := []string{exe, "-state", *statePath, "-api", *apiAddr}
		log.Fatalf("restarting: %v", syscall.Exec(exe, args, os.Environ()))
	}

	a := newAPI(sensor)
	a.replace = replace
	go func() {
		log.Printf("API on %s", *apiAddr)
		log.Fatal(http.ListenAndServe(*apiAddr, a))
	}()

	sensor.Tick(time.Now())
	for now := range time.Tick(5 * time.Second) {
		sensor.Tick(now)
	}
}
