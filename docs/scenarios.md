# Manual scenarios for LibreTransmitter in Loop

Eight scenarios to run libre2sim against a real Loop build (LoopWorkspace with the patched LibreTransmitter and ImpossiBLE
injected; see the README's "Run it with LibreTransmitter in Loop"). Each one gives the curl commands to steer the sensor, what
LibreTransmitter and Loop should do, and a verification mark.

Verification marks: **cbharness** means `tools/cbharness` (real CoreBluetooth, real ImpossiBLE, LibreTransmitter's real
`Libre2DirectTransmitter` plugin) can cover the scenario's BLE side. **Needs Loop** means only a full Loop build shows the result
(alerts, the app relaunching, the chart). Each scenario ends with what has been run so far.

## Setup used by all of them

```bash
export API=http://127.0.0.1:8081           # libre2sim's -api
go build -o libre2sim .
./libre2sim -fresh -age 3h                 # scenarios 1-4, 6, 7; see each for other flags
curl -s $API/status                        # the sensor at any time
```

`/status` is the ground truth to compare Loop with: `glucose` (the modelled value of this minute), `lifeCount` (minutes since
activation), `unlockCount` (the count of the last accepted BLE unlock), `connected`, `advertising`, `fault`.

`-age` takes a Go duration, which has no days: 14 days 23 hours is `-age 359h`. A sensor wears 15 days (21600 minutes) by default.

Loop shows a reading every 5 minutes. LibreTransmitter makes a reading from the last few minutes of trend data and smooths it, so a
change in glucose takes about 25 minutes (five readings) to settle fully in Loop. libre2sim makes a packet every minute.

---

## 1. Fresh sensor: pair, then readings

```bash
./libre2sim -fresh -age 3h
```

In Loop: add a CGM, choose LibreTransmitter, then "Libre 2" direct, and scan. (In the Simulator the scan is a request to libre2sim
over HTTP; the console shows `NFC reading FRAM`.)

Expect:

- libre2sim logs `NFC identifier`, `NFC reading FRAM`, an enable-streaming command, then `unlocked streaming with count 1` and
  `sent the packet for minute N` every minute.
- LibreTransmitter reports the sensor serial from `/status`, state "ready", and the sensor's age of about 180 minutes.
- Within 5 minutes Loop shows a glucose value; after about 25 minutes it matches `/status` `glucose` within 1 mg/dL when the mode is
  constant (`curl -s -XPOST $API/glucose -d '{"kind":"fixed","value":120}'`). In the default sine mode the trend arrow follows the
  curve.

Verification: cbharness: **passed** 2026-11-03 (pairing, then two packets with CRC valid and glucose = model). Needs Loop: not yet run (the first reading in Loop).

---

## 2. Kill and relaunch Loop: reconnect without a rescan

With scenario 1 running and readings flowing, note the unlock count:

```bash
curl -s $API/status | grep unlockCount
xcrun simctl terminate <simulator> <Loop's bundle id>
# relaunch with the same SIMCTL_CHILD_* environment as before (README)
curl -s $API/status | grep -E 'unlockCount|connected'
```

Expect: Loop reconnects through its stored `preSelectedDevice`, with no pairing and no rescan. `unlockCount` goes up by one for each
connection (LibreTransmitter's count only increases), and `connected` is `true` again. Readings resume with the next minute's
packet. Add `-strict-unlock` to libre2sim to make the sensor refuse a replayed count; a normal reconnect must still work.

To check the part where iOS relaunches a terminated app for a lodged connect, start libre2sim with
`-simulator <simulator> -relaunch` and terminate the app; `POST $API/force-quit` models the user swiping the app away, after which
libre2sim must not relaunch it.

Verification: cbharness: **passed** 2026-11-03 (with `-strict-unlock`: counts 1→2→3 accepted; `REPLAY=2` refused: hang-up, re-advertise). Needs Loop: not yet run
(the relaunch).

---

## 3. Out of range

```bash
curl -s -XPOST $API/out-of-range -d '{"minutes":20}'
```

Expect: the link drops at once, `advertising` is `false` for 20 minutes, and Loop gets no readings. After 20 minutes libre2sim
advertises again, LibreTransmitter reconnects by itself, and readings resume with an unlock count one higher. LibreTransmitter does
no BLE backfill, so the 20-minute gap stays in Loop's chart. That is expected.

`curl -s -XPOST $API/disconnect` is the short version: a drop that LibreTransmitter reconnects from within seconds. It answers 409
when no phone is connected.

Verification: cbharness: **passed** 2026-11-03 (`RECONNECT=1 FOLLOW=1`: out of range for 2 minutes, the app's connect is held, then
advertise, connect, unlock, packets resume; this needed ImpossiBLE patch 0008, without which no reconnect from `didDisconnect` ever
discovered its services). Needs Loop: not yet run (the gap in the chart).

---

## 4. Low glucose

```bash
curl -s -XPOST $API/glucose -d '{"kind":"fixed","value":55}'
```

Expect: readings fall to 55 mg/dL over the next minutes (the new mode applies from the next minute on) and Loop's low glucose
alert fires once the smoothed value is below the threshold. Then back to normal:

```bash
curl -s -XPOST $API/glucose -d '{"kind":"sine","value":120,"amplitude":40,"periodMinutes":240}'
```

Values are held to 40 to 400 mg/dL, the range of the sensor's readings.

Verification: cbharness: **passed** 2026-11-03 (the next minute decodes to 55 mg/dL; earlier minutes keep the old model). Needs Loop: not
yet run (the alert).

---

## 5. End of wear

Near the end:

```bash
./libre2sim -fresh -age 359h          # 14 days 23 hours: 60 minutes left
```

Expect: LibreTransmitter's sensor-expiry warning in Loop, with the remaining time falling each minute. Past the end (an hour
later, or quicker with `-age 359h50m`): libre2sim sends no more packets, hangs up the link and stops advertising; the FRAM says
state 05. Loop's readings stop and LibreTransmitter reports the sensor as expired or ended.

Early termination is the same from the sensor's side:

```bash
curl -s -XPOST $API/terminate
```

Expect: `framState` 5, `terminated` in `/status`, the link drops, no more packets. Starting a new sensor is scenario 7.

Verification: cbharness: **passed** 2026-11-03 (terminate: hang-up, no more packets, the reconnect is held, a new scan fails with NFC
error `0xB0`; expiry with `-age 359h58m`: packets for minutes 21598 and 21599, then the hang-up at 21600). Needs Loop: not yet run (the
alerts).

---

## 6. Faults

A bad checksum:

```bash
curl -s -XPOST $API/fault -d '{"kind":"badcrc"}'     # /status now has "fault":"badcrc"
```

Expect: the next packet has an inverted CRC in its plaintext. LibreTransmitter's `decryptBLE` throws, and
`Libre2DirectTransmitter.handleCompleteMessage` swallows the error in an empty `catch {}`. The packet is **dropped silently**:
no `checksumValidationError` reaches the delegate (that path only runs when decryption succeeds but `parseBLEData`'s own CRC
check fails, which a corrupted CRC can't produce). Loop gets no reading for that minute and recovers with the following packet. libre2sim
logs `sending the packet for minute N with fault badcrc`, and `fault` is gone from `/status`.

A short packet (a second case worth running):

```bash
curl -s -XPOST $API/fault -d '{"kind":"short"}'
```

Expect: the next packet goes out as 20 + 20 + 5 bytes, 45 in all. LibreTransmitter parses its receive buffer only at exactly 46
bytes, so its buffer then holds 45 stale bytes until its next reset (a reconnect); the packets that follow do not parse, and
readings stop until you `POST $API/disconnect` and it reconnects. That is the fault's point: it shows what an app does with a
truncated packet.

`{"kind":"none"}` disarms a fault before it fires. An unknown kind answers 400.

Verification: cbharness: **passed** 2026-11-03.
- badcrc: the packet for minute 180 was dropped silently, with no error callback; minute 181 parsed normally.
- short: after the 45-byte packet for minute 181, the full packets for 182 and 183 arrived but were never parsed (the buffer is stuck
  above 46 bytes). This confirms that one truncated notify stalls LibreTransmitter until the next reconnect, which is a robustness bug
  worth reporting upstream.

Needs Loop: not yet run (how Loop shows it).

---

## 7. A new sensor

```bash
curl -s -XPOST $API/new -d '{"wearDays":15,"warmupMinutes":60,"ageMinutes":180}'
```

libre2sim restarts as the new sensor (a new UID, serial and BLE address). Expect: the app's stored sensor has the old UID and the old
BLE name, so it does not find the new one: no readings, and a connect to the old peripheral is held, never answered, as iOS
holds a connect to a sensor that is out of range. After you remove the sensor in Loop and scan again, the new UID is paired, the
calibration is the new one and readings resume.

`ageMinutes` 0 gives a sensor still in its applicator: scan it in the app, or `curl -s -XPOST $API/activate` (409 if it is
already started) to start it by hand. It warms up for `warmupMinutes`, and LibreTransmitter shows no readings until then.

Verification: cbharness: **passed** 2026-11-03 (a reconnect with the old sensor finds nothing; a new scan pairs the new MAC and
streams). Needs Loop: not yet run.

---

## 8. Beside libre3sim, in one app

Run both emulators, each with its own socket and API port, and join the sockets with g7sim's `impossible-mux` (its README has the
flags):

```bash
./libre2sim -fresh -age 3h -api 127.0.0.1:8081 -socket /tmp/impossible.sock
../libre3sim/libre3sim -fresh -age 3h -api 127.0.0.1:8080 -socket /tmp/impossible-libre3.sock
# impossible-mux: serve /tmp/impossible.sock, forwarding to the two sockets above
```

Launch Loop with `SIMCTL_CHILD_IMPOSSIBLE_SOCKET=/tmp/impossible.sock`, `SIMCTL_CHILD_LIBRE2SIM_URL=http://127.0.0.1:8081` and
`SIMCTL_CHILD_LIBRE3SIM_URL=http://127.0.0.1:8080`.

Expect: Loop has both plugins; a scan for a Libre 2 reaches libre2sim, and a Libre 3 pairing reaches libre3sim. A Libre 2's advertised name is its MAC and it
advertises FDE3; the Libre 3's service is its own, so neither scan matches the other's sensor. Each `/status` shows `connected` for
its own sensor only.

Verification: cbharness: not yet run (not applicable: one plugin). Needs Loop: not yet run.
