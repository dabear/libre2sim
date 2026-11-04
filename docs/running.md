# Running libre2sim

Step-by-step notes for developers. There are three ways to run it, from quickest to fullest:

1. [libre2sim on its own](#1-libre2sim-on-its-own), with its control panel (2 minutes)
2. [With LibreTransmitter's real code, without Loop](#2-with-libretransmitters-real-code-without-loop), via `tools/cbharness` (15 minutes)
3. [In Loop, in the iOS Simulator](#3-in-loop-in-the-ios-simulator) (a full Loop build)

> libre2sim is a development tool. It is not an Abbott product and not a medical device. All its data is made up.
> Never use it, or an app connected to it, for therapy decisions.

## Requirements

- macOS with Xcode (tested with Xcode 27.2, iOS 17+ Simulator runtimes) and Go 1.20 or later.
- If `GO111MODULE` is off in your environment, prefix Go commands with `GO111MODULE=on`.
- Unix socket paths must be under 104 bytes on macOS, so keep sockets in `/tmp`.

## 1. libre2sim on its own

```bash
cd libre2sim
GO111MODULE=on go build -o libre2sim .
./libre2sim -fresh -age 3h          # a sensor worn for 3 hours, ready to scan
```

Open **http://localhost:8081/** for the control panel. A dark sidebar (it collapses, and is a drawer on a phone) leads
to four pages:

- **Dashboard**: the sensor minute and wear left, the phone's connection, unlocks and the glucose model; the current
  glucose (mg/dL and mmol/L) with its trend, the **next reading** with a countdown to when it is sent, a chart, and the
  latest events. The next value is exact for `fixed` and `sine`. For `walk` it's an estimate (≈), because the walk's
  noise is drawn when the minute arrives.

  The chart shows the last 60 minutes, a line marking **now**, and the **next 15 readings** as a dashed line. Above it
  you can widen it to 8 hours, 24 hours or the **whole wear** (the warmup at the left, the wear still to come at the
  right, up to "wear ends"); the panel remembers your choice. The longer ranges fetch one reading every few minutes, so
  hovering there shows the reading only, not the raw values. For `walk`
  the coming readings are the expected path, with a band where about 95% of the random readings will fall. Markers
  show where a warmup ends ("first reading") or the wear ends within that window. **Hover over (or tap) a minute** for
  its reading and the raw values its BLE record carries: the raw glucose, what LibreTransmitter computes from it, the
  raw temperature fields, and the 4-byte record, each with a short explanation.
- **Sensor**: change the glucose model (sine, fixed, random walk, or one of your sensor recordings) or the skin temperature (and how far the tissue lags it), drop the link or go out
  of range, spoil the next packet, activate, terminate or replace the sensor; and the full status.
- **Event log**: every NFC command and answer (with hex and error codes), BLE connects, unlocks, packets, faults and sensor
  events. Filter it by NFC, BLE or Sensor, and pause it to read.
- **Settings**: a strict-unlock toggle, the launch flags, and the sensor's identity (UID, serial, patch info, BLE address,
  calibration `i1`…`i6`, and the fixed temperature every measurement carries).

The same actions are available as `curl` commands; see the README's "Control API". On its own, libre2sim streams
nothing: a phone (or the harness) must scan it over `/nfc` first, which enables Bluetooth streaming, just as on a
real Libre 2.

Useful flags: `-age 0` (the default) leaves the sensor in its applicator (scan it or `POST /activate`); `-warmup 5m` shortens
the warmup of a new sensor (60 minutes by default; "End warmup now" on the Sensor page or `POST /end-warmup` cuts a running
one short); `-age 359h58m` gives a sensor 2 minutes before the end of its 15 days; `-strict-unlock` refuses a replayed unlock
count. `-state` is where the sensor is saved; without `-fresh` libre2sim resumes it.

### Sensor recordings (traces)

Besides the made-up models, libre2sim can play back a real sensor's readings: a **trace**, made from one FRAM dump
of a Libre 1 or Libre 2 (EU) sensor (344 bytes, unencrypted, base64-encoded). libre2sim decodes the dump's 8 hours of
history and its last 15 minutes with LibreTransmitter's own algorithm and the sensor's own calibration, fills in the
minutes between the 15-minute history points smoothly, and plays the result in a loop. On a running sensor, choosing
a recording also replaces the readings made so far, lined up so the recording ends at the current minute: the chart
shows it at once, as it was recorded. From the next minute it loops, with a 15-minute bridge from its last reading back
to its first. (On a sensor still in warmup it starts at the first reading.) An app that already read the old minutes
keeps them; a new scan or backfill gets the recording.

Traces are local files: `traces/<name>.txt` beside the binary (or `-traces DIR`). git ignores that directory, so
recordings never end up in the repository. Lines starting with `//` are comments: the first one names the recording
in the panel (`// Sensor 1, recorded 2018-04-07`), and `// currentbg: 92` notes what the reader showed.

The Sensor page's model list shows every trace in the directory as its own choice. To add your own, open **Add your
own sensor recording** below it, or `POST /traces` (see the README's "Control API"). A dump with the wrong size or a
failing CRC (an encrypted Libre 2 dump, for example) is refused with the reason.

## 2. With LibreTransmitter's real code, without Loop

`tools/cbharness` runs in the iOS Simulator with real CoreBluetooth (through ImpossiBLE) and LibreTransmitter's real
pairing code and `Libre2DirectTransmitter` plugin. It's the quickest way to see the whole path work.

```bash
W=/tmp/l2work; mkdir -p $W            # any scratch directory

# LibreTransmitter next-dev (it has the Simulator pairing change from #62, 924e933, on)
git clone -b next-dev https://github.com/LoopKit/LibreTransmitter $W/LT

# The ImpossiBLE library (clones ImpossiBLE at a pinned commit and applies simulator/impossible-patches)
sh /path/to/libre2sim/simulator/build-impossible.sh $W/imp      # → $W/imp/libImpossiBLE.dylib

# cbharness: the swiftc command in tools/cbharness/README.md, run inside $W/LT, with -o $W/cbharness

# Run
./libre2sim -fresh -age 3h &
xcrun simctl boot "iPhone 16"
SIMCTL_CHILD_DYLD_INSERT_LIBRARIES=$W/imp/libImpossiBLE.dylib \
SIMCTL_CHILD_PACKETS=2 xcrun simctl spawn "iPhone 16" $W/cbharness       # ends with RESULT: OK
```

Watch the panel's event log while it runs: the NFC scan, the connect, the unlock and one packet a minute. The harness's
modes (`RECONNECT=1`, `FOLLOW=1`, `REPLAY=n`, `TIMEOUT=s`) and the scenarios they cover are in `tools/cbharness/README.md`
and [scenarios.md](scenarios.md).

## 3. In Loop, in the iOS Simulator

```bash
# 1. Put the LibreTransmitter inside your LoopWorkspace checkout on next-dev: the Simulator pairing change is there
#    from #62 (924e933) on. For an older LibreTransmitter (main, or a LoopWorkspace that pins f3e232e or earlier),
#    apply integration/LibreTransmitter/0001-Pair-with-libre2sim-in-the-iOS-Simulator.patch with `git am -3` instead.
cd LoopWorkspace/LibreTransmitter
git fetch origin && git checkout origin/next-dev

# 2. Start libre2sim and build ImpossiBLE (as above)
./libre2sim -fresh -age 3h
sh /path/to/libre2sim/simulator/build-impossible.sh /tmp/imp

# 3. The app needs the library: ./start.sh sets DYLD_INSERT_LIBRARIES inside every booted Simulator for you
#    (launchctl setenv; the library stays inert in the Simulator's own processes, impossible-patches/0009), and
#    removes it on Ctrl-C. By hand, either set it for the whole Simulator:
xcrun simctl spawn booted launchctl setenv DYLD_INSERT_LIBRARIES /tmp/imp/libImpossiBLE.dylib
#    or only in Loop's scheme (Edit Scheme → Run → Arguments → Environment Variables; don't commit it), or per launch:
SIMCTL_CHILD_DYLD_INSERT_LIBRARIES=/tmp/imp/libImpossiBLE.dylib xcrun simctl launch booted <Loop's bundle identifier>
#    Use the path's real letter case: the Simulator's dyld matches it case-sensitively.
#    IMPOSSIBLE_SOCKET and LIBRE2SIM_URL are only needed if you change -socket or -api.
```

In Loop, go to Settings → add a CGM → **FreeStyle Libre** → the Libre 2 direct connection, and scan. The "scan" calls
libre2sim's `/nfc` instead of NFC. The app then finds the sensor over Bluetooth by its advertised name and receives a
reading each minute (Loop uses one every 5 minutes).

The change is inert on a phone: everything it adds is behind `#if targetEnvironment(simulator)`. `tools/devicecheck` shows
the device build's code is unchanged.

## When something doesn't work

| Symptom | Cause |
|---|---|
| Pairing works but the app never connects; the panel shows no `connect` | the app doesn't have the ImpossiBLE library. It was already running when `./start.sh` set it up (quit it and run it again), the Simulator was booted after `./start.sh` (run it again), or the path's letter case differs from the real one (the Simulator's dyld is case-sensitive; `start.sh` uses `/bin/pwd -P`) |
| The scan fails with "libre2sim is not running at …" | libre2sim isn't listening on `LIBRE2SIM_URL` (default `127.0.0.1:8081`) |
| The scan fails with NFC error `0xB0` | the sensor is still in its applicator (`POST /activate`), ended, or terminated (`POST /new` for another) |
| The app never finds the sensor | it advertises only after a scan enabled streaming, while no phone holds the link and it is in range. Check `advertising` in the panel |
| Nothing reaches libre2sim over Bluetooth | ImpossiBLE's menu bar app holds the same socket (quit it, or use a socket of your own with `-socket`), or the dylib wasn't injected |
| The log says "no valid unlock in 1m0s" after each reconnect | an ImpossiBLE build without `simulator/impossible-patches/0008` (an app that reconnects from `didDisconnect` never got its services). Rebuild with `build-impossible.sh` |
| Readings stop after a `short` fault | expected: LibreTransmitter parses its buffer only at exactly 46 bytes and stalls until it reconnects (`POST /disconnect`) |
| `bind: invalid argument` for the socket | the socket path is 104 bytes or longer |
| Changing the glucose had no effect yet | changes apply from the next sensor minute; the panel's "next reading" shows what comes |
