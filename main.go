// Command libre2sim emulates a FreeStyle Libre 2 Plus (EU) sensor for
// LibreTransmitter. For development use only: not an Abbott product, and its
// data is made up.
package main

import (
	"flag"
	"log"
	"net/http"
	"os"
	"strconv"
	"syscall"
	"time"

	"libre2sim/libre2"
	"libre2sim/suspend"
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
	socket := flag.String("socket", "/tmp/impossible-libre2.sock", "ImpossiBLE socket the app in the Simulator connects to")
	strictUnlock := flag.Bool("strict-unlock", false, "accept a BLE unlock only if its count is above the last one accepted")
	simulator := flag.String("simulator", "", "the app's simulator (name or UDID), for -relaunch and -suspend")
	relaunch := flag.Bool("relaunch", false, "relaunch a terminated app for its lodged connect, as iOS does")
	suspendApp := flag.Bool("suspend", false, "stop the app while iOS would have it suspended, waking it for Bluetooth events")
	dylib := flag.String("dylib", "", "ImpossiBLE library injected into a relaunched app (default: simulator/build/libImpossiBLE.dylib beside libre2sim)")
	flag.Parse()
	if (*relaunch || *suspendApp) && *simulator == "" {
		log.Fatalf("-relaunch and -suspend need -simulator")
	}
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
	sensor.SetStrictUnlock(*strictUnlock)

	// The BLE radio: the ImpossiBLE socket an app in the iOS Simulator
	// talks CoreBluetooth through.
	radio, err := NewSimRadio(sensor, *socket)
	if err != nil {
		log.Fatalf("serving %s: %v", *socket, err)
	}
	if *relaunch {
		radio.Relaunch = simctlRelauncher(*simulator, *dylib)
	}
	if *suspendApp {
		radio.Suspend = suspend.New(*simulator)
	}

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
		radio.Stop()
		if err := next.Save(*statePath); err != nil {
			log.Fatalf("saving %s: %v", *statePath, err)
		}
		exe, err := os.Executable()
		if err != nil {
			log.Fatalf("restarting: %v", err)
		}
		log.Printf("restarting as the new sensor")
		args := []string{exe, "-state", *statePath, "-api", *apiAddr, "-socket", *socket,
			"-strict-unlock=" + strconv.FormatBool(*strictUnlock), "-simulator", *simulator,
			"-relaunch=" + strconv.FormatBool(*relaunch), "-suspend=" + strconv.FormatBool(*suspendApp),
			"-dylib", *dylib}
		log.Fatalf("restarting: %v", syscall.Exec(exe, args, os.Environ()))
	}

	a := newAPI(sensor)
	a.radio = radio
	a.replace = replace
	go func() {
		log.Printf("API on %s", *apiAddr)
		log.Fatal(http.ListenAndServe(*apiAddr, a))
	}()

	// The radio's clock also ticks the sensor.
	radio.Run()
}
