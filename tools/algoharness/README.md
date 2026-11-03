# algoharness

Runs a LibreTransmitter checkout's **own glucose algorithm** (`GlucoseFromRaw.swift` and what it needs) on records
libre2sim made, and compares each minute with the glucose libre2sim intended. Use it to see what an algorithm change does
before it reaches a phone: for example temperature compensation, temperature smoothing, or limits.

libre2sim makes each record's raw glucose at the **tissue** temperature, and gives it the **skin** temperature the
thermistor reads (`POST /temperature`). With no tissue lag the two are the same, and LibreTransmitter's current algorithm
gets every reading back exactly. With a lag, a skin temperature change reaches the raw glucose gradually, as the
experimental temperature smoothing assumes. The harness shows how wrong each algorithm is during and after such a change.

## Scenarios

`scenarios` builds a sensor on a simulated clock (no waiting) and writes its last 60 minutes, as `GET /readings` returns
them, for each scenario:

```bash
GO111MODULE=on go run ./tools/algoharness/scenarios /tmp/sc
```

| Scenario | What happens |
|---|---|
| `steady` | 31.6 °C throughout, glucose 120 |
| `cold-steady` | 24 °C skin and tissue throughout |
| `step-no-lag`, `step-lag5`, `step-lag10` | skin 31.6 → 27 °C; the tissue follows at once, over 5, or over 10 minutes |
| `blip-no-lag`, `blip-lag5` | skin 31.6 → 29 °C for 3 minutes (a cold draught) |
| `falling-sine` | glucose falling through a sine, 120 ± 60 |
| `low-35` | glucose 35 mg/dL, below the sensor's 40–500 range |

You can also use any live libre2sim: `curl -s 'http://127.0.0.1:8081/readings?minutes=60' > /tmp/live.json`.

## Build and run

```bash
# LibreTransmitter's next-dev (or any checkout with the same algorithm API)
tools/algoharness/build.sh /path/to/LibreTransmitter /tmp/algo-nextdev
# a checkout with the experimental GlucoseAlgorithm type (temperature smoothing)
tools/algoharness/build.sh /path/to/LibreTransmitterExperimental /tmp/algo-exp -D EXPERIMENTAL

SIMCTL_CHILD_READINGS=/tmp/sc/step-lag5.json xcrun simctl spawn booted /tmp/algo-nextdev
```

It prints a table per variant (minute, wanted, skin and tissue °C, what the algorithm got, the error) and a `SUMMARY` line:
mean error, mean |error| and max |error| in mg/dL.

Env:
- `READINGS`: the `/readings` JSON file (required).
- `QUIET=1`: only the summary lines.
- `BATCH=libre2` (experimental build): compute each minute from just its own Libre 2 BLE packet (the readings 0, 2, 4,
  6, 7, 12 and 15 minutes old), which is all the app's temperature filter sees. Without it, the filter runs over all 60
  minutes at once, a best case.

The harness covers the per-reading algorithm only. Glucose smoothing (`LibreGlucose`), the reportable range (40–500) and
rate limiting are applied later in the app and aren't part of it.
