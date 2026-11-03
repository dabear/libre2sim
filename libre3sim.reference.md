# libre3sim — reference

How [LoopKit/libre3sim](https://github.com/LoopKit/libre3sim) emulates a FreeStyle Libre 3 sensor,
and how it lines up with the two client repos it was written against:

- [airedev326/RoundWhiteDiscKit](https://github.com/airedev326/RoundWhiteDiscKit) — the Swift package
  that implements the Libre 3 protocol client (NFC, BLE handshake, data plane). Its `protocol.md` is
  the protocol write-up; its decoders are the spec libre3sim encodes against.
- [LoopKit/LibreLoop](https://github.com/LoopKit/LibreLoop) — the Loop `CGMManager` plugin built on
  RoundWhiteDiscKit. Under `#if targetEnvironment(simulator)` it swaps CoreNFC and Abbott trust for
  libre3sim's HTTP API.

Snapshot analysed: default branches as of 2026-11-03 (libre3sim is ~3.5k lines of Go, no deps
outside the stdlib).

---

## 1. Big picture

```
 iOS Simulator                                     macOS host
┌──────────────────────────────┐                 ┌──────────────────────────────────────┐
│ Loop + LibreLoop plugin      │                 │ libre3sim (Go)                        │
│   RoundWhiteDiscKit          │                 │                                       │
│                              │  HTTP :8080     │  api.go  ── /nfc  ──► Sensor.NFC()    │
│   LibreLoopSimulatedNFCReader├────────────────►│          ── /app-credentials          │
│   LibreLoopSimulator         │                 │          ── /patch-signing-key        │
│                              │                 │          ── /glucose /terminate …     │
│   CoreBluetooth calls        │                 │                                       │
│     │ (swizzled by           │  Unix socket    │  impossible.go  SimRadio              │
│     ▼  libImpossiBLE.dylib)  ├────────────────►│    GATT table, advertising, connect   │
│   ImpossiBLE shim            │  NDJSON         │    └─ simLink ──► libre3.handler      │
└──────────────────────────────┘                 │         (handshake + data plane)      │
                                                 │  libre3/ Sensor + State (JSON file)   │
                                                 └──────────────────────────────────────┘
```

Two channels, because the Simulator has neither NFC nor Bluetooth:

| Real world | libre3sim stand-in |
|---|---|
| NFC tap on the sensor (ISO 15693 custom commands) | `POST /nfc` with `{code, parameters}` |
| Abbott-signed app certificate the app ships with | `GET /app-credentials` (libre3sim's own CA signs it) |
| Abbott patch-signing public key the app trusts | `GET /patch-signing-key` |
| BLE radio | [ImpossiBLE](https://github.com/mickeyl/ImpossiBLE) dylib injected into the app; libre3sim is the "provider" on its Unix socket |

The app code above the transport (RoundWhiteDiscKit's `PairingFlow`, `SensorSession`, decoders) runs
**unchanged** — only the NFC reader, the trust anchors and the CoreBluetooth backend are swapped.

### Source map

| File | Role |
|---|---|
| `main.go` | flags, load/create keys + state, start API and radio; `/new` re-`exec`s itself with a fresh state file |
| `api.go`, `web.go` | HTTP control API + tiny HTML control panel |
| `impossible.go` | `SimRadio`: ImpossiBLE provider — GATT table, scan/advert, connect, read/write/notify, iOS state-restoration/relaunch emulation |
| `suspend/` | SIGSTOP/SIGCONT the app process to mimic iOS background suspension (from g7sim) |
| `libre3/state.go` | persisted `State` (serial, BLE address, keys, PIN, receiver, every minute's glucose…) + glucose models |
| `libre3/sensor.go` | `Sensor`: **NFC command handling**, lifecycle, patch status, realtime/historic/clinical record builders |
| `libre3/handler.go` | per-connection BLE state machine: security handshake, CCCD arming, per-minute streaming, `patchControl` |
| `libre3/frames.go` | fragmentation, data-plane nonce/seal, wire layouts of every plaintext record |
| `libre3/keys.go` | emulator trust root, app/sensor certificates, ECDH → kAuth |
| `libre3/ccm.go` | AES-128-CCM with 4-byte tag (hand-rolled, 7- or 13-byte nonce) |
| `libre3/link.go` | `Char` enum and `Link`/`Handler` interfaces between transport and sensor |
| `simulator/build-impossible.sh` + `impossible-patches/` | builds `libImpossiBLE.dylib` at a pinned commit with 7 fix-up patches |

---

## 2. NFC layer (the focus)

### 2.1 How the app reaches it

On device, RoundWhiteDiscKit's `Libre3NFCActivationReader` (CoreNFC, `NFCTagReaderSession` polling
`.iso15693`) calls `tag.customCommand(requestFlags: [.highDataRate], customCommandCode: code,
customRequestParameters: params)`. CoreNFC inserts the manufacturer code `0x7a` (Abbott) itself, so
the app supplies only the command code and the parameter bytes. The raw request on air would be
`02 <code> 7a <params…>` (`NFCActivationCommand.command(...)`).

In the Simulator, LibreLoop uses `LibreLoopSimulatedNFCReader` (`LibreLoop/Pairing/LibreLoopSimulator.swift`),
a line-for-line twin of the CoreNFC reader's `run(tag:)` whose `customCommand` is replaced by:

```http
POST $LIBRE3SIM_URL/nfc            (default http://127.0.0.1:8080)
{"code": 161, "parameters": ""}                          # 0xa1, hex params

200 {"response": "00a5…"}                                # what CoreNFC would hand back
422 {"error": "NFC error 0xb1: …", "code": 177}          # tag error
```

`api.go` decodes the hex, calls `Sensor.NFC(code, params, now)` and returns the bytes hex-encoded.
On a `422` the Swift side throws `LibreLoopSimulator.Failure(message:)` with the error text — the
numeric code is not mapped to a typed CoreNFC error.

Pairing selects the reader at compile time (`LibreLoopPairingService.swift:316`):

```swift
#if targetEnvironment(simulator)
let nfcReader = LibreLoopSimulatedNFCReader()
#else
let nfcReader = Libre3NFCActivationReader()
#endif
```

### 2.2 The scan sequence

Every scan mode does the same two steps:

1. **`0xa1` read patch info** (no params) → `Libre3NFCPatchInfo`.
2. Unless mode is `.readPatchInfo`: pick **`0xa0` activate** or **`0xa8` switch receiver** and send the
   10-byte body → `Libre3NFCActivationResponse`.

Command choice (`Libre3NFCPatchInfo.recommendedCommandCode`): state byte `0x01` → `0xa0`, anything
else → `0xa8`. LibreLoop uses `.activateOrSwitchReceiver(receiverID: stableID)` for a new pairing and
`.switchReceiver(receiverID: id)` for recovery with a known receiver ID.

### 2.3 `0xa1` — patch info (29 bytes)

Built by `Sensor.patchInfo()`; parsed by `Libre3NFCPatchInfo(raw:)`.

| Off | Len | libre3sim value | Client field |
|---:|---:|---|---|
| 0 | 1 | `00` | must be `00` (after normalisation) |
| 1 | 1 | `a5` | must be `a5` |
| 2 | 1 | `00` | — |
| 3 | 2 LE | `SecurityVersion` = 1 | `securityVersion` |
| 5 | 2 LE | `Region` = 1 | `region` (1 EU, 2 US, 4 AU/CA, 8 RoW) |
| 7 | 2 LE | `Generation` = 1 | `generation` |
| 9 | 2 LE | `WearMinutes` (14 d = 20160) | `wearDurationMinutes` |
| 11 | 4 | `Firmware` `[30,2,4,1]` | shown as `b14.b13.b12.b11` → `1.4.2.30` |
| 15 | 1 | `ProductType` = 4 | `productType` |
| 16 | 1 | `WarmupMinutes / 5` (60 min → 12) | `warmupMinutes = byte * 5` |
| 17 | 1 | NFC state (below) | `stateByte` |
| 18 | 9 | ASCII serial | `serialNumber` |
| 27 | 2 LE | `abbottCRC16(b[2:27])` | not checked by the client (sim's choice) |

> Note: RoundWhiteDiscKit's `protocol.md` table lists firmware at offset 13 and a "raw status" byte at
> 12; the actual parser (and libre3sim) use firmware at 11–14, product type 15, warmup 16. Trust the code.

The client's `normalizeResponse` tolerates a missing leading `00` and repeated `a5` bytes; libre3sim
always sends the clean `00 a5 …` form.

**NFC state byte** (`nfcState`). Only `0x01` is grounded in captures; the rest are libre3sim's guess
borrowed from the Libre 2. Any value ≠ `01` just makes the client send `0xa8`.

| Byte | Condition (evaluated in this order) |
|---|---|
| `01` not activated | `Activated == nil` (still in applicator; `-fresh` without `-age`) |
| `05` terminated | `Terminated != nil` |
| `04` expired | life count ≥ wear minutes |
| `02` warmup | life count < warmup minutes |
| `03` active | otherwise |

### 2.4 `0xa0` / `0xa8` — activate / switch receiver

**Request params** (10 bytes, `NFCActivationCommand.metcrc`):

```
timeSeconds_LE4 || receiverID_LE4 || abbottCRC16(first 8)_LE2
```

`timeSeconds` defaults to `now − 1` (Unix seconds). `receiverID` is the 4-byte identity the sensor
binds to (LibreLoop: a stable per-install ID kept in the keychain; RoundWhiteDiscKit also has folds
from a LibreView account UUID, see `Libre3ReceiverID.swift`).

**abbottCRC16** (identical in Go and Swift): CRC-CCITT, poly `0x1021`, init `0xffff`, each input byte
bit-reversed before it enters the register, no final reflection/xor, written little-endian.

**Validation in `Sensor.NFC`** (in order):

| Check | Error |
|---|---|
| params length ≠ 10 | `0x02` "want 10 bytes" |
| CRC mismatch | `0x02` "bad CRC" |
| `0xa0` on an activated sensor | `0xb0` "already activated; switch receiver instead" |
| `0xa8` on an unactivated sensor | `0xb0` "not activated yet" |
| `0xa8` with a receiver ≠ stored `ReceiverID` (and stored ≠ 0) | `0xb1` "receiver … is not the one this sensor was activated with" |
| any other command code | `0x01` "unknown command" |

`0xb1` mirrors real-sensor behaviour that RoundWhiteDiscKit documents: a sensor remembers its
activating receiver and refuses a switch from any other.

**Side effects on success:**

- `0xa0`: `Activated = now` (truncated to the second — the request's `timeSeconds` is **ignored**),
  `ReceiverID = receiver`. The life-count clock starts; warmup begins.
- `0xa8`: if `ReceiverID == 0` (sensor created with `-age`, i.e. "unclaimed"), the first switch claims it;
  otherwise receiver stays as is.
- Both: **a new random 4-byte BLE PIN** is generated and `PairingOpen = true`. State is saved.

The "burn the PIN, open pairing once" rule is the key coupling between NFC and BLE:

- The BLE Phase 5 must carry the PIN from the *latest* NFC response, so an older PIN stops working.
- A **full** BLE handshake (certificates + ephemerals) is accepted only while `PairingOpen` is true.
  Completing one sets it back to false, so a second full handshake needs another scan. (Cached
  reconnects with the stored kAuth don't need it — see §3.3.)

**Response** (19 bytes, `activationResponse()`; parsed by `Libre3NFCActivationResponse`):

| Off | Len | Content |
|---:|---:|---|
| 0 | 3 | `00 a5 00` |
| 3 | 6 | BLE address, **reversed** (little-endian) — client reverses for display `E4:64:47:6E:2C:FE` |
| 9 | 4 | BLE PIN |
| 13 | 4 LE | activation time (Unix s) — the *original* activation time, also on `0xa8` |
| 17 | 2 LE | `abbottCRC16(b[3:17])` — sim's choice; client keeps it as `trailingCRC` but does not verify |

The client requires exactly 19 bytes with `00 a5 00`. From this it builds `Libre3SensorState`
(serial, PIN, BLE address, receiver ID, warmup/wear from patch info).

### 2.5 From NFC to BLE: finding the sensor

iOS hides peripheral MAC addresses, so the sensor **advertises its BLE address as the local name**
(uppercase hex, no colons, e.g. `E464476E2CFE`) alongside service `089810CC-…`. The app matches the
NFC-returned address to the advertised name. libre3sim:

- `State.BLEAddress` — random 6 bytes with the top two bits set (random static address).
- `discovered()` sends `kCBAdvDataLocalName = AdvertisedName()`, `kCBAdvDataServiceUUIDs = [data service]`.
- The ImpossiBLE peripheral UUID is `UUIDv4-shaped(sha256("libre3sim " + name))`, stable across restarts.
- A sensor only advertises once activated (`Activated != nil`), while not connected, not in a staged
  outage, and not shut down.

### 2.6 NFC scenarios you can stage

| Goal | How |
|---|---|
| Fresh sensor, first pairing (`0xa1` → state `01` → `0xa0`) | `./libre3sim -fresh` |
| Already-worn sensor, unclaimed (`0xa8` claims it) | `./libre3sim -fresh -age 3h` |
| Recovery with wrong receiver → `0xb1` | pair once, then reinstall the app with a different receiver ID |
| Full handshake without a scan → fails at Phase 6 | scan, pair, then force a second full handshake |
| Expired/terminated state byte | `-days`/`-age` past wear, or `POST /terminate` then scan |
| Replace the sensor (new serial + address) | `POST /new {"wearDays":15,"warmupMinutes":60,"ageMinutes":0}` |

Direct poke without the app:

```bash
curl -s -XPOST localhost:8080/nfc -d '{"code":161,"parameters":""}'          # 0xa1
# 0xa0 needs a valid 10-byte body: time LE4 || receiver LE4 || abbottCRC16 LE2
```

`handler_test.go` (`TestFreshSensorActivates`, `TestActivationPairingReconnectAndBackfill`) exercises
exactly these paths.

---

## 3. BLE layer

### 3.1 GATT table (`impossible.go`, matches `LibreSensorGATT.swift`)

All UUIDs are `0898XXXX-EF89-11E9-81B4-2A2AE2DBCCE4`.

| Service | Char | Props | Role |
|---|---|---|---|
| `10CC` data | `177A` glucoseData | notify | realtime reading each minute (2 notifies: 15 + rest) |
| | `1482` patchStatus | read, notify | 12-byte status |
| | `1338` patchControl | write, notify | backfill / shutdown commands |
| | `195A` historicData | notify | 5-min history pages |
| | `1BEE` eventLog | notify | (not emitted) |
| | `1AB8` clinicalData | notify | per-minute clinical records |
| | `1D24` factoryData | notify | (not emitted) |
| `203A` security | `23FA` secCert | write, notify | certificates, ephemeral keys |
| | `22CE` secChallenge | write, notify | Phase 5/6 |
| | `2198` secCommand | write, notify | 1-byte state-machine clock |
| `2400` debug | — | | |

A Libre 3 holds **one** link and stops advertising while connected.

### 3.2 Fragmentation (`frames.go`)

- Phone → sensor writes: `offset_LE2 || ≤18 bytes`; `writeBuffer.feed` reassembles, offset 0 resets.
- Sensor → phone handshake notifies: `seq1 || ≤19 bytes`.
- Data-plane frames are sent whole (glucose split in two notifies).

### 3.3 Security handshake (`handler.go`)

Command-gated: every step is a 1-byte write on `secCommand`, answered on the same char.

| Phone writes cmd | Sensor does |
|---|---|
| `01` StartAuthentication | reset; this is a full handshake |
| `02` LoadCertificate | then phone writes 162-byte app cert on `secCert` |
| `03` SendCertificateLoadDone | verify cert against emulator CA → notify `04`, else **drop link** |
| `09` GetCertificate | notify `0a`, then 140-byte sensor cert on `secCert` |
| `0d` ValidateCertificate | following `secCert` writes are the phone's ephemeral key (65 B used) |
| `0e` SendEphemeralDone | gen ephemeral, derive kAuth, notify `0f`, then sensor ephemeral pubkey |
| `11` StartAuthorization | (cached reconnect starts here, using saved kAuth) notify `08`, then `R1(16) ‖ nonce(7)` on `secChallenge` |
| `08` SendChallengeLoadDone | phone wrote Phase 5 → verify → notify `08`, then Phase 6 |

- **kAuth** = `SHA-256(00000001 ‖ ECDH(sensorEph, phoneEph) ‖ ECDH(sensorStatic, appStatic))[:16]`.
- **Phase 5** (phone): `AES-CCM(kAuth, nonce7, R1 ‖ R2 ‖ PIN4)` — 36 B ct + 4 B tag (+14 zero pad). Sensor
  checks tag, R1 and **the NFC-issued PIN**; any mismatch drops the link.
- **Phase 6** (sensor): `AES-CCM(kAuth, nonce7', R2 ‖ R1 ‖ kEnc16 ‖ ivEnc8) ‖ nonce7'`. `ivEnc` is
  `00000000 ‖ random4`.
- Full handshake while `PairingOpen == false` → Phase 6 is sealed under a **random key**, so the
  phone fails to verify it (models a real sensor rejecting a re-pair without a scan).
- On success: full handshake stores kAuth (`State.AuthKey`) and closes pairing; data plane starts.
- 60 s idle timeout before authorization → hang up.

### 3.4 Trust (`keys.go`)

`libre3sim-keys.json` holds two P-256 scalars made on first run: a **signing key** (stand-in for
Abbott's CA) and an **app key**. Nothing is Abbott material.

- App cert (162 B): `03 03 ‖ 00..0f ‖ "libre3sim app  " ‖ appPub65 ‖ sig64` — served via
  `/app-credentials` in the `AppIdentities.install(json:)` format.
- Sensor cert (140 B): `01 ‖ sha256(serial)[:10] ‖ sensorStaticPub65 ‖ sig64`.
- Sig = ECDSA-P256/SHA-256 raw `r‖s` over everything before it.
- LibreLoop (Simulator builds only) installs the credential in `LibreLoopRuntimeTables.install()` and
  passes `/patch-signing-key` as `sensorCertSigningKeys` to `PairingFlow`.

### 3.5 Data plane

After Phase 6, nothing streams until the phone enables CCCDs on **both glucoseData and patchStatus**
(RoundWhiteDiscKit deliberately subscribes data chars only after the handshake). Then:

- Frame = `AES-CCM(kEnc, nonce13, plaintext) ‖ seq_LE2`, tag 4 B.
- `nonce13 = seq_LE2 ‖ descriptor3 ‖ ivEnc8`; `seq` is per-characteristic, starting at 1.
- Descriptors: patchControl `000000`, patchStatus `0000f0`, glucose `000f00`, historic `00f000`,
  clinical `0f0000`, eventLog `f00000`, factory `440000` (last three guessed; client tries all).

Every minute (1 s ticker, emits when life count advances): patchStatus, realtime glucose, and a
clinical record if subscribed.

| Record | Size | Layout (LE) |
|---|---:|---|
| patchStatus | 12 | lifeCount2 ‖ errorData2 ‖ eventData2 ‖ index1 ‖ patchState1 ‖ currentLifeCount2 ‖ 00 00 |
| glucoseData | 29 | lc2 ‖ glucose2 ‖ rate×100 (i16) ‖ 00 00 ‖ projected15min×100 ‖ histLC2 ‖ histGlucose2 ‖ trend\|0x08 ‖ glucose2 ‖ histGlucose2 ‖ temp2 (2590) ‖ zeros |
| historic page | 14 | startLC2 ‖ 6 × glucose2 (5 min apart) |
| clinical | 14 | lc2 ‖ raw1=g×16 ‖ raw2=g×12 ‖ 0x0e00 ‖ 00 00 ‖ glucose2 ‖ historic2 |

- Trend: rate ≤ −2 → 1, ≤ −1 → 2, < 1 → 3, < 2 → 4, else 5 (mg/dL/min, from reading 5 min earlier).
- "Committed historic" point at lc = `(lc − 17)` rounded down to a multiple of 5.
- Patch states: 4 active (also warmup), 5 expired (errorData 5), 6 terminated, 8 replace sensor, 3/7.

`patchControl` writes (7-byte decrypted command):

| Cmd | Action |
|---|---|
| `01 00 .. fromLC_LE2` | historical backfill ≥ fromLC → historic pages |
| `01 01 .. fromLC_LE2` | clinical backfill ≥ fromLC |
| `05 …` | shutdown → terminate (errorData 6), send status |

Clinical stream remembers `ClinicalSent`; on resubscribe it replays the gap (max 6 h).

---

## 4. Sensor model & persistence

`libre3state.json` (atomic tmp+rename on every new minute):

- Identity: serial (9 chars, starts with `3`), BLE address, static key, firmware etc.
- Lifecycle: `Activated`, `WearMinutes`, `WarmupMinutes`, `Terminated`, `LastEvent`.
- Pairing: `ReceiverID`, `PIN`, `PairingOpen`, `AuthKey`.
- `Readings[]`: one `uint16` per minute of wear (0 during warmup), filled lazily up to now and clamped
  40–400. Because past values are stored, backfill returns consistent history.
- Glucose models (`POST /glucose`): `sine` (default 120 ± 40, 240 min), `fixed`, `walk`
  (mean-reverting random walk, 2 % pull, σ 0.8). Changing mode affects only future minutes.

Life count = whole minutes since activation.

---

## 5. Transport: ImpossiBLE (`impossible.go`)

- ImpossiBLE swizzles CoreBluetooth in the app and talks newline-delimited JSON over a Unix socket
  (default `/tmp/impossible.sock`, override per-app with `IMPOSSIBLE_SOCKET` via patch 0007).
- libre3sim is the provider: handles `hello`, `scan`, `connect`, `discoverServices/Characteristics`,
  `read`, `write`, `setNotify`, `cancel`, `readRSSI`; replies with `didDiscover`, `didConnect`,
  `didUpdateValue`, `didDisconnect`, …
- Main loop every 100 ms: tick the sensor, re-advertise to an active scan every 5 s, complete a
  pending `connect` as soon as the sensor is advertising (iOS-style lodged connect).
- Connects to a peripheral ID that isn't the current sensor are held forever (out-of-range old sensor).
- Emulates iOS lifecycle (from g7sim): state restoration of held connect/scan, `-relaunch` of a
  terminated app via `simctl`, `-suspend` SIGSTOP/SIGCONT with 10 s BLE wakes, and logs
  "NO WAKE PATH" when the sensor advertises but the app has no pending connect/scan.
- `build-impossible.sh` builds ImpossiBLE @ `58fe38d` + patches (delegate queue, UUID matching,
  subscriptions/services cleared on disconnect, `retrievePeripheralsWithIdentifiers`, socket env var).

---

## 6. Control API (`:8080`)

| Endpoint | Effect |
|---|---|
| `GET /` | control panel |
| `GET /status` | serial, address, nfcState, PIN, receiver, pairingOpen, connected, advertising, glucose… |
| `POST /glucose` | `{"kind":"fixed\|sine\|walk","value":…,"amplitude":…,"periodMinutes":…}` |
| `POST /disconnect` | drop the link; app reconnects |
| `POST /out-of-range` | `{"minutes":n}` — link drops, no adverts, then backfill on return |
| `POST /terminate` | `{"errorData":6\|8\|3}` — early end; stops advertising after link closes |
| `POST /event` | event-log entry carried in patchStatus while the sensor keeps working |
| `POST /new` | new sensor (re-execs the process with a fresh state) |
| `POST /nfc` | NFC tag stand-in (§2) |
| `GET /app-credentials`, `GET /patch-signing-key` | trust material (§3.4) |

---

## 7. Running it

```bash
go build -o libre3sim .
./libre3sim -fresh                    # sensor in applicator; -age 3h for a worn one; -warmup 5m
simulator/build-impossible.sh         # prints path of libImpossiBLE.dylib
SIMCTL_CHILD_DYLD_INSERT_LIBRARIES=/path/libImpossiBLE.dylib \
  xcrun simctl launch <sim> <Loop bundle id>
```

Then add a FreeStyle Libre 3 in Loop and "scan" — the scan is `POST /nfc`. Quit ImpossiBLE's menu-bar
app first or use `-socket` + `SIMCTL_CHILD_IMPOSSIBLE_SOCKET` + `SIMCTL_CHILD_LIBRE3SIM_URL`.

---

## 8. Notes for a Libre 2 port (this repo)

What carries over almost as-is:

- The **architecture**: `Sensor` (state + NFC) / per-link `handler` / transport behind `Link`+`Handler`
  interfaces, so the BLE transport (ImpossiBLE today, maybe a Pi later) is swappable.
- `impossible.go` + `suspend/` + the patched ImpossiBLE build (transport-agnostic, just change
  `simServices` and the advert).
- The NFC-over-HTTP pattern: the app has a `#if targetEnvironment(simulator)` reader that POSTs
  `{code, parameters}` and gets back the exact bytes CoreNFC would return — keep the sim honest by
  encoding against the client's real parser.
- Persisted per-minute readings so backfill/history stay consistent; staged outages/termination via API.
- Separate trust anchors served over HTTP when the protocol needs signed material.

What must be redone for Libre 2: the NFC command set and responses (Libre 2 is FRAM-read based with
its own patch-info/unlock/enable-streaming commands and encrypted FRAM), the BLE GATT table and
advert, and the streaming crypto (no certificate/ECDH handshake like Libre 3). Use the target
client (e.g. the Libre 2 decoder in the app you're testing) as the spec, the way libre3sim uses
RoundWhiteDiscKit.

### Gaps / sim-specific choices to be aware of in libre3sim

- NFC state bytes other than `01` are guesses; `timeSeconds` in `0xa0/0xa8` is ignored.
- Patch-info and activation-response CRCs are computed by the sim but not checked by the client.
- eventLog and factoryData channels are never emitted; descriptors for clinical/eventLog/factory guessed.
- Raw clinical channels are fabricated (`g×16`, `g×12`).
- A Phase 5 that fails to verify drops the link (the real sensor's behaviour is unknown).
- No real-radio transport yet ("Raspberry Pi BLE transport: not yet").
