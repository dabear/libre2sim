# libre2sim — plan for a Libre 2+ emulator that LibreTransmitter can pair with

Goal: a Libre 2 Plus (EU) sensor emulator, in the style of [libre3sim](libre3sim.reference.md), that
[LoopKit/LibreTransmitter](https://github.com/LoopKit/LibreTransmitter) can pair with and stream from
inside the iOS Simulator, with its real code running.

Based on LibreTransmitter `main` @ `4110512` (2026-09-26). File references are to that repo.

---

## 1. What LibreTransmitter expects from a Libre 2 sensor

Libre 2 "direct" (phone ↔ sensor, no MiaoMiao/Bubble bridge) has two stages: an NFC pairing scan,
then a BLE stream.

### 1.1 NFC pairing scan (`LibreSensor/SensorPairing/SensorPairingService.swift`)

One `NFCTagReaderSession` (`.iso15693`) runs these steps in order:

| Step | CoreNFC call | Sensor must return |
|---|---|---|
| 1 | `tag.identifier` | 8-byte UID. The app reverses it: `sensorUID = identifier.reversed()`. Real UIDs end `… a4 07 e0` in sensorUID order. |
| 2 | `getSystemInfo` | Any success (the result is ignored). |
| 3 | `customCommand(0xA1, [])` | **6-byte patch info.** It must be ≥ 6 bytes, and the later check is `== 6`. |
| 4 | `readMultipleBlocks` 0…42, 3 blocks per request | **43 × 8 = 344 bytes of encrypted FRAM.** |
| 5 | `customCommand(0xA1, [0x1E, code LE4, d4])` | Enable streaming. **6 bytes = BLE MAC, little-endian.** The app reverses it and turns it into uppercase hex: `macAddress`. |

How the app checks and uses the result:

- `SensorType(patchInfo:)` must be `.libre2`, so `patchInfo[0] ∈ {0x9D, 0xC5, 0xC6, 0x7F}`
  (`Bluetooth/LibreTransmitterMetadata.swift:109`). `patchInfo[2] >> 4 == 3` gives family "Libre 2",
  which the serial number needs.
- The checks are `sensorUID.count == 8`, `patchInfo.count == 6` and `fram.count == 344`. Otherwise the
  app reports `noSensorData`.
- `Libre2.decryptFRAM(type: .libre2, id: uid, info: patchInfo, data: fram)`
  (`LibreSensor/SensorContents/PreLibre2.swift`). The keystream per 8-byte block is
  `processCrypto(prepareVariables(uid, x: block, y: (info[5]<<8|info[4]) ^ 0x44))`.
- The enable-streaming command uses `code = 42` (hardcoded `unlockCode`) and
  `d = usefulFunction(uid, x: 0x1E, y: (info[5]<<8|info[4]) ^ (b[1]<<8|b[0]))`, where `b` is the code LE.
- From the decrypted FRAM (`SensorData.swift`):
  - Calibration: `i1`, `i2` from bits at byte 2; `i3`, `i4`, `i5`, `i6` and the sign of `i3` from bits at
    byte `0x150`.
  - `maxMinutesWearTime = footer[6..7]` becomes `Sensor.maxAge`.
  - The footer CRC is saved as `CalibrationToSensorMapping.reverseFooterCRC`. Every later BLE packet is
    only accepted while that mapping still matches (`LibreTransmitterManager+Libre2EU.swift:59`).
- The direct path does **not** check the FRAM CRCs or the state byte. We should still make them correct.
- LibreTransmitter never activates a sensor. It assumes the sensor is already started.

### 1.2 Finding the sensor over BLE (`Bluetooth/Transmitter/LibreTransmitterProxyManager.swift`)

- The scan filters on service UUIDs (`getServicesForDiscovery`). For Libre 2 that is **`FDE3`**.
- On `didDiscover`, while `preSelectedUid` is set (first connect after the NFC scan), the peripheral is
  accepted if either check passes:
  1. **`peripheral.name == macAddress`** from the NFC step (exact match, uppercase hex, no colons). The
     code comment says Libre 2 Plus sensors in Europe identify themselves this way since mid-2025.
  2. Legacy: 8-byte `kCBAdvDataManufacturerData` where bytes `[2..8]` plus `07 e0` equal the UID, and
     the name starts with `ABBOTT` or contains the MAC.
- After that it stores `peripheral.identifier` (`preSelectedDevice`) and reconnects through
  `retrievePeripherals` and state restoration.

**Libre 2+ choice:** advertise **name = MAC hex** with service `FDE3`. That avoids manufacturer data,
which ImpossiBLE may not pass through (to verify).

### 1.3 BLE session (`Bluetooth/Transmitter/Libre2DirectTransmitter.swift`)

| Item | Value |
|---|---|
| Service | `FDE3` |
| Write characteristic | `F001` (write with response) |
| Notify characteristic | `F002` |
| On discovering `F002` | `setNotifyValue(true)` |
| On discovering `F001` | Write the **12-byte unlock payload** |
| Then | Sensor notifies a **46-byte encrypted packet** each minute. The app appends chunks and parses when the buffer is **exactly 46 bytes**. |

**Unlock payload** (`Libre2.streamingUnlockPayload`, `enableTime: 42`, `unlockCount` goes up by one
per connection and is stored in UserDefaults):

```
time = 42 + unlockCount
b    = time LE4
ad   = usefulFunction(uid, 0x1B, 0x1B6A)
ed   = usefulFunction(uid, 0x1E, (42 & 0xFFFF) ^ info[4..5])
t2   = processCrypto(prepareVariables(uid, ed01^b23, ad01, ed23^b01, ad23))
t3x  = crc16(...) of fixed bytes c1c4c3c0d4e1e7ba ‖ t2 / ad / ed / b   (Libre2-local crc16)
res  = processCrypto(prepareVariables(uid, t31..t34))  → 8 bytes
payload = b(4) ‖ res(8)
```

**Packet** (`Libre2.decryptBLE`, `parseBLEData`):

```
46 bytes = n0 n1 ‖ ct44
key: x = (d[1]<<8|d[0]) ^ (d[3]<<8|d[2]) | 0x63, with d = usefulFunction(uid, 0x1B, 0x1B6A)
     y = (n1<<8|n0) ^ 0x63
     k0 = processCrypto(prepareVariables(uid, x, y)); keystream = 8 × (k_i → 8 bytes, k_{i+1} = processCrypto(k_i))
pt44 = ct44 XOR keystream
pt44 = 10 × record4 ‖ age LE2 (bytes 40-41) ‖ crc16 BE (bytes 42-43, Crc.crc16 seed 0xffff over pt[0..41])
record (bits LSB-first): 0-13 rawGlucose, 14-25 rawTemp>>2, 26-30 tempAdj>>2, 31 tempAdj sign
records 0-6: trend at age − {0,2,4,6,7,12,15} min
records 7-9: history at ((age−2)/15)*15 − 15·(i−7)
```

The two nonce bytes `n0 n1` are free for the sensor to choose. The emulator can use a counter or
random values.

### 1.4 Turning raw values into mg/dL (`LibreSensor/GlucoseAlgorithm/GlucoseFromRaw.swift`)

The app computes glucose from `rawGlucose`, `rawTemperature`, `rawTemperatureAdjustment` and the FRAM
calibration:

```
R    = rawTemp·72500 / (tempAdj + i6) − 1000
T    = 1 / (cd·ln³R + cc·ln²R + cb·lnR + ca) − 273.15
g1   = 65·(raw − i3)/(i4 − i3)
g3   = g1 · 1.045^(32.5 − T)
mgdl = (g3 − t1[i2−1]) / t2[i2−1]          (then round, × extraSlope + extraOffset)
```

This can be inverted. With a fixed calibration and fixed temperature fields, T is a constant, so

`raw = i3 + (i4 − i3)/65 · (mgdl·t2[i2−1] + t1[i2−1]) / 1.045^(32.5−T)`, rounded to an integer and
held to 14 bits.

The emulator needs this inverse so that the glucose Loop shows equals the glucose the sim models.

The app reports only the newest trend value to Loop (once every ≥ 4.5 min). It uses the other trend
values for smoothing, the trend arrow and the prediction.

---

## 2. The one change LibreTransmitter needs

The Simulator has no NFC. Two things block pairing:

- `Features.phoneNFCAvailable` (`NFCNDEFReaderSession.readingAvailable`) is false, so
  `SensorPairingService.pairSensor()` throws `nfcNotSupported`, and the device list hides NFC-only
  transmitters (`BluetoothSelection.swift:150`).
- `LibreTransmitterManagerV3.pairingService` (`:322`) always returns the CoreNFC `SensorPairingService`.
  `MockSensorPairingService` exists, but only SwiftUI previews use it, and its fixed xDrip FRAM has no
  matching BLE sensor.

Proposed PR, the same pattern as LibreLoop's `LibreLoopSimulator.swift`:

1. Add `LibreSensor/SensorPairing/SimulatedSensorPairingService.swift` under
   `#if targetEnvironment(simulator)`. It conforms to `SensorPairingProtocol` and runs the same 5 steps
   from §1.1 against `$LIBRE2SIM_URL` (default `http://127.0.0.1:8081`). It then calls the **same**
   `Libre2.decryptFRAM` and builds the **same** `SensorPairingInfo`. Only the transport changes.
2. In `pairingService`, `#if targetEnvironment(simulator) return SimulatedSensorPairingService()`.
3. In `Features.phoneNFCAvailable`, return `true` in the Simulator.

That is about 100 lines of Swift. Nothing else changes, because BLE goes through ImpossiBLE exactly as
it does for LibreLoop and libre3sim.

**NFC-over-HTTP API (proposed):** one thin endpoint that stands in for each CoreNFC call, so the
client stays a faithful twin of the real reader:

```http
POST /nfc {"op":"identifier"}                              → {"response":"<8 bytes hex, tag order>"}
POST /nfc {"op":"systemInfo"}                              → {"response":""}
POST /nfc {"op":"readBlocks","first":0,"count":3}          → {"blocks":["<8B>", …]}
POST /nfc {"op":"custom","code":161,"parameters":"<hex>"}  → {"response":"<hex>"} | 422 {"error","code"}
```

---

## 3. libre2sim architecture (reusing libre3sim)

```
main.go, api.go, web.go        ← copy from libre3sim, change routes/flags (port 8081, -socket /tmp/impossible-libre2.sock)
impossible.go, suspend/        ← copy as-is; swap simServices + discovered()
libre2/
  crypto.go     processCrypto, prepareVariables(id,x,y), prepareVariables(uid,i1..i4), usefulFunction,
                Libre2-local crc16 (table + reverse + byteswap), Libre Crc.crc16
  fram.go       build 344-byte FRAM (header/body/footer + CRCs), encrypt (XOR, same keystream as decrypt)
  nfc.go        identifier / systemInfo / readBlocks / A1 (patch info) / A1+1E (enable streaming, verify d)
  unlock.go     verify the 12-byte unlock write
  packet.go     46-byte packet: records, age, CRC, encryption
  glucose.go    inverse of GlucoseFromRaw for a fixed calibration
  handler.go    per-link: wait for F002 subscribe + valid unlock, then notify a packet each minute
  state.go      persisted State (UID, patchInfo, MAC, activation, wear, unlock code/count, readings[])
```

What carries over from libre3sim unchanged in spirit: per-minute `Readings[]` persisted to JSON,
glucose modes (`fixed`/`sine`/`walk`), `/disconnect`, `/out-of-range`, `/terminate`, `/new`
(re-exec), one link at a time with no advertising while connected, held connects for old peripherals,
the iOS relaunch and suspend emulation, and the "NO WAKE PATH" warning.

GATT table for `impossible.go`:

```go
var simServices = []gattService{
    {"FDE3", []gattChar{
        {"F001", propWrite, libre2.CharWrite},
        {"F002", propNotify, libre2.CharNotify},
    }},
}
// advert: kCBAdvDataLocalName = MAC hex (uppercase, no colons), kCBAdvDataServiceUUIDs = ["FDE3"]
```

ImpossiBLE patch 0003 ("Match service UUIDs in any of their forms") already handles a 16-bit `FDE3`
against the app's scan filter.

---

## 4. Sensor model choices (Libre 2+)

| Item | Choice | Confidence |
|---|---|---|
| UID | 8 random bytes ending `a4 07 e0` (sensorUID order); `tag.identifier` = reversed | high (matches every sample in repo) |
| patchInfo | `[0xC6, 0x09, 0x31, 0x01, r, r]`. `[0]` ∈ {9D,C5,C6,7F}, `[2]>>4 == 3`, `[4..5]` random (keys the crypto) | `[0]` value for 2+ to verify with a real dump; any listed value passes |
| MAC | random static address; NFC returns it LE; advert name = uppercase hex | high (code path explicit) |
| Wear | 15 days = 21600 min in `footer[6..7]` | to verify (grace period?) |
| Warmup | 60 min. During warmup, stream records with raw = 0 (the app skips 0) | sim's choice |
| FRAM state byte `header[4]` | `0x03` ready (`0x02` warmup, `0x05` expired/shutdown) | from LibreTransmitter's `SensorState` |
| FRAM age `body[292..293]` | current minutes since start | high |
| Calibration | Take i1…i6 from a real dump (decrypt `FakeSensorPairingData`, which is xDrip's sample) | high |
| Temperature fields | Fixed plausible values from the same real dump | high |
| Unlock check | `time == 42 + n` with `n` > last accepted; otherwise drop the link | real sensor behaviour unknown; make "strict" a flag |
| Enable streaming on a non-activated sensor | NFC error (no MAC) | sim's choice |
| Packet cadence | Every minute; chunks of ≤ 20 bytes adding up to exactly 46 (20+20+6) | the app needs exactly 46 per message |

---

## 5. Build order

1. **Crypto spike + test vectors.** Port `processCrypto`/`prepareVariables`/`usefulFunction` and both
   CRCs to Go. Tests:
   - Decrypt `Libre2.Example`, `Example2` and the xDrip FRAM in `FakeSensorPairingData`. All three FRAM
     CRCs must validate.
   - Decrypt `Libre2.BLEExample` (uid `2fe7b10000a407e0`). The packet CRC must validate.
   - Round-trip FRAM and packet encryption.
   - Compute `usefulFunction` and the unlock payload in Go, check them against a tiny Swift harness or
     `swift test` on a copy of `PreLibre2.swift`.
2. **FRAM + NFC model** with `go test` coverage: build FRAM from state, encrypt, serve the `/nfc` ops,
   verify the `0x1E` command's `d`.
3. **LibreTransmitter simulator hook** (§2). Check that pairing completes in the Simulator and that
   `calibrationMapping` and `preSelectedSensor` get stored.
4. **BLE.** Copy `impossible.go`, add the libre2 handler (subscribe → unlock → stream), and the packet
   builder with the glucose inverse. End-to-end: Loop shows the sim's value within ±1 mg/dL after
   5-point smoothing settles.
5. **Lifecycle + faults:** expiry (the app's expiry alerts use `maxAge − age`), `/terminate`,
   `/out-of-range`, `/disconnect`, a bad-CRC packet (the app should show `checksumValidationError`), a
   wrong unlock count, and replacing the sensor (the calibration mapping no longer matches, so the app
   reports `noCalibrationData`).
6. **Docs:** README run steps like libre3sim's, with its own `-socket` and `LIBRE2SIM_URL`, plus
   `impossible-mux` so it can run beside libre3sim.

---

## 6. Risks / open questions

- **Real Libre 2+ fingerprint.** We need one real Libre 2+ dump to confirm `patchInfo[0]`, the wear
  minutes and the advert. LibreTransmitter accepts every value we'd pick, so this matters for realism,
  not for compatibility.
- **Unlock replay rules** on a real sensor are unknown. Default to lenient, offer strict as a flag.
- **ImpossiBLE manufacturer data.** It's only needed if we also want to test the legacy `ABBOTT…`
  matching path.
- **Licensing.** `GlucoseFromRaw.swift` and the Libre 2 crypto come from community projects. Check the
  licences before porting them into Go, and credit the sources as libre3sim does.
- **Upstream acceptance** of the small Simulator hook in LibreTransmitter. libre3sim needed the same
  thing in LibreLoop, so there is precedent.

### Oddities noticed in LibreTransmitter (they won't block the sim, but the sim can surface them)

- `parseBLEData` stamps trend record *i* at `now − i minutes` even though record *i* is `age − {0,2,4,6,7,12,15}[i]`
  minutes old. Records 1–6 get the wrong timestamps. Record 0, the one Loop uses, is correct.
- The README says `GetGlucoseFromRaw.swift` is not included, but `GlucoseFromRaw.swift` is in the repo.
- A notify that pushes the receive buffer past 46 bytes is never parsed or reset until the next
  connection. Chunks must add up to exactly 46.
