# libre2sim: implementation plan

Background and protocol notes are in [libre2-plan.md](libre2-plan.md). libre3sim is described in
[libre3sim.reference.md](libre3sim.reference.md). This file is the build order: what to write, in
which file, how to test it, and when each step counts as done.

**Target:** LoopKit/LibreTransmitter `main` @ `4110512`, running inside Loop in the iOS Simulator,
pairs with a simulated Libre 2+ (EU) sensor and shows its glucose.

**Ground rules (same as libre3sim)**

- Go, stdlib only. One binary.
- The client code is the spec. Each encoder is tested by decoding its output with a Go port of the
  client's decoder, which is checked against the vectors in LibreTransmitter.
- Where the client doesn't pin a behaviour down, pick one and say so in a code comment.
- No Abbott key material and no real sensor dumps beyond what LibreTransmitter already ships. Only
  simulated data.

---

## Milestone 0: Scaffold (½ day)

| Task | Detail |
|---|---|
| 0.1 | `git init`. Add `go.mod` (`module github.com/<owner>/libre2sim`, go 1.22). Add `.gitignore` (`libre2sim`, `*.json` state, `simulator/build/`), MIT `LICENSE`, and a README stub with the libre3sim-style disclaimer. |
| 0.2 | Copy from libre3sim (MIT, keep credits): `impossible.go`, `impossible_test.go`, `suspend/`, `simulator/build-impossible.sh`, `simulator/impossible-patches/`, `web.go`. Copy the `main.go`/`api.go` skeletons. |
| 0.3 | Create the `libre2/` package with `link.go`, which keeps libre3sim's `Link`/`Handler` interfaces, and `Char` = `{CharNone, CharWrite, CharNotify}`. |

**Done when** `go build ./... && go vet ./...` pass. libre3-specific code can be stubbed until M4.

---

## Milestone 1: Crypto and CRCs, proven against LibreTransmitter's vectors (1 day)

`libre2/crypto.go`, ported line for line from `LibreSensor/SensorContents/PreLibre2.swift`:

```go
var key = [4]uint16{0xA0C5, 0x6860, 0x0000, 0x14C6}

func processCrypto(in [4]uint16) [4]uint16
func prepareVariables(uid []byte, x, y uint16) [4]uint16             // the (id:, x:, y:) variant
func prepareVariables4(uid []byte, i1, i2, i3, i4 uint16) [4]uint16 // the (sensorUID:, i1…i4) variant
func usefulFunction(uid []byte, x, y uint16) [4]byte
func infoWord(patchInfo []byte) uint16                               // UInt16(info[5], info[4])
```

`libre2/crc.go` holds one CRC16. `Crc.crc16` in `CRC.swift` (with seed 0xffff) and the private
`crc16` at the bottom of `PreLibre2.swift` compute the same thing: table CRC, seed 0xffff, then
bit-reverse and byte-swap. They differ only in how the callers use the result:

```go
// FRAM header/body/footer CRCs: stored BE in the first 2 bytes of each region.
// BLE packet CRC: stored BE in the last 2 bytes of the 44-byte plaintext.
// Unlock payload: the caller byte-swaps the result once more (t31…t34).
func libreCRC16(b []byte) uint16
```

`libre2/fram.go` (crypto part):

```go
func cryptFRAM(uid, patchInfo, fram []byte) []byte // XOR with the per-block keystream; works both ways
```

`libre2/packet.go` (crypto part):

```go
func bleKeystream(uid []byte, n0, n1 byte) [64]byte
func decryptPacket(uid, pkt46 []byte) ([]byte, error) // port of decryptBLE, used in tests
func encryptPacket(uid []byte, n0, n1 byte, pt44 []byte) []byte
```

`libre2/unlock.go`:

```go
func unlockPayload(uid, patchInfo []byte, enableTime uint32, count uint16) [12]byte // port of streamingUnlockPayload
func enableStreamingD(uid, patchInfo []byte, code uint32) [4]byte                  // d in SensorPairingService.nfcCommand
```

**Tests** (`crypto_test.go`, `vectors_test.go`). Copy the byte arrays from
`PreLibre2.swift` (`Example`, `Example2`, `BLEExample`) and `FakeSensorPairingData.swift`:

1. `cryptFRAM(Example.sensorId, Example.sensorInfo, Example.buffer)`: the header, body and footer CRCs
   all validate with `libreCRC16`. Same for `Example2` and the xDrip FRAM in `FakeSensorPairingData`.
   *Check the UID byte order here: `Example.sensorId` is already in sensorUID order.*
2. `decryptPacket(BLEExample.sensorId, BLEExample.data)` gives a valid packet CRC.
3. Round trips: `cryptFRAM(cryptFRAM(x)) == x`, and `decryptPacket(encryptPacket(pt)) == pt`.
4. **Golden values from Swift.** In `tools/swiftvectors/`, add a ~40-line `swift` script that pastes in
   `PreLibre2.swift`'s `Libre2` enum and the CRC code. It prints `usefulFunction`, `unlockPayload`
   (uid = BLEExample, info = Example2, time 42, count 1…3) and `enableStreamingD` as hex. The output
   is committed to `testdata/swift_vectors.json`, and the Go tests compare against it. That is the only
   way to check the unlock payload without a real sensor.

**Done when** all of the above pass. If vector 1 fails, stop and fix the port before going on.

---

## Milestone 2: Sensor state, FRAM builder, NFC model (1½ days)

`libre2/state.go`:

```go
type State struct {
    UID           string     // 8 bytes hex, sensorUID order (… a4 07 e0)
    PatchInfo     string     // 6 bytes hex
    BLEAddress    string     // display order "AA:BB:…"
    WearMinutes   int        // 21600
    WarmupMinutes int        // 60
    Activated     *time.Time // nil = not started (LibreTransmitter can't start one)
    StreamingCode *uint32    // unlock code from the last 0x1E; nil = streaming not enabled
    UnlockCount   uint16     // last accepted BLE unlock count (strict mode)
    Terminated    *Termination
    Calibration   Calibration // i1…i6, rawTemp, tempAdj: taken from the decrypted xDrip FRAM
    Glucose       GlucoseMode // reuse libre3sim's fixed/sine/walk
    Readings      []uint16    // mg/dL per minute of wear, 0 in warmup
}
func NewState(wearDays int, warmup, age time.Duration) (*State, error)
```

- UID: 5 random bytes followed by `a4 07 e0`. PatchInfo: `C6 09 31 01 rr rr` (sim's choice; any of
  9D/C5/C6/7F passes, `[2]>>4 == 3`). MAC: random static address.
- Serial number: port `SensorSerialNumber.swift`, used for display only.

`libre2/fram.go` (builder):

```go
func (s *State) plainFRAM(now time.Time) []byte // 344 bytes
```

| Region | Contents |
|---|---|
| header 0–23 | CRC (0–1 BE), byte 2 bits: i1 (0–2), i2 (3–12), `header[4]` state: 01 / 02 warmup / 03 ready / 05 ended |
| body 24–319 | CRC, `body[2]` next trend index, `body[3]` next history index, 16×6 trend ring at body+4, 32×6 history ring at body+100, `body[292..293]` = minutes since start |
| footer 320–343 | CRC, `footer[6..7]` = WearMinutes, calibration bits at byte `0x150` (= footer+16): i3, i4, sign, i5, i6 |

The simplest correct approach: start from the decrypted xDrip FRAM as a template, overwrite the
fields above, then recompute all three CRCs. That keeps every unknown byte realistic. The trend and
history rings use the 6-byte `Measurement(bytes:)` layout (port its bit reads). The direct path
ignores them, but the MiaoMiao/Bubble path and the debug UIs read them.

`libre2/nfc.go`:

```go
func (s *Sensor) NFCIdentifier() []byte                     // reversed UID (tag order)
func (s *Sensor) NFCReadBlocks(first, count int) ([][]byte, error)
func (s *Sensor) NFCCustom(code byte, params []byte, now time.Time) ([]byte, error)
```

`NFCCustom(0xA1, …)`:

- Empty params → 6-byte patch info.
- `[0x1E, code LE4, d4]`:
  - Verify `d == enableStreamingD(uid, info, code)`. On mismatch return `NFCError{0x02}`.
  - If the sensor isn't activated or has ended, return `NFCError{0xB0}` (sim's choice).
  - Otherwise store `StreamingCode = code` and return the MAC **LE** (6 bytes).
- Any other subcommand or code returns `NFCError{0x01}`.

**Tests** (`nfc_test.go`, `fram_test.go`):

- `decryptFRAM(plainFRAM ⊕ crypt)` parses with a Go port of `SensorData.calibrationData`. The i1…i6
  values come back unchanged, and `maxMinutesWearTime == WearMinutes`.
- The 0x1E command built exactly as `SensorPairingService.nfcCommand` builds it (code 42) gets the
  right MAC, and the reversed hex equals `AdvertisedName()`.
- A wrong `d` gets an error. An unactivated sensor refuses enable-streaming.

**Done when** the tests pass. A Go "fake client" test (`client_test.go`) runs the five pairing steps
in order and ends with a `SensorPairingInfo`-equivalent whose calibration and footer CRC are
consistent.

---

## Milestone 3: LibreTransmitter Simulator hook (1 day, separate repo/PR)

Branch in LibreTransmitter: `simulator-libre2sim`.

1. **New file** `LibreSensor/SensorPairing/SimulatedSensorPairingService.swift`:

   ```swift
   #if targetEnvironment(simulator)
   import Foundation
   import Combine

   /// The Simulator has no NFC; pairing talks to libre2sim's HTTP stand-in for the sensor's tag.
   public final class SimulatedSensorPairingService: SensorPairingProtocol {
       private let subject = PassthroughSubject<SensorPairingInfo, Never>()
       public var onCancel: (() -> Void)?
       public var publisher: AnyPublisher<SensorPairingInfo, Never> { subject.eraseToAnyPublisher() }
       static var baseURL: URL { URL(string: ProcessInfo.processInfo.environment["LIBRE2SIM_URL"] ?? "http://127.0.0.1:8081")! }

       public func pairSensor() throws {
           Task {
               do { let info = try await scan(); await MainActor.run { subject.send(info) } }
               catch { print("libre2sim pairing failed: \(error)"); await MainActor.run { onCancel?() } }
           }
       }

       private func scan() async throws -> SensorPairingInfo {
           let sensorUID = Data(try await op(["op": "identifier"]).reversed())
           _ = try await op(["op": "systemInfo"])
           let patchInfo = try await op(["op": "custom", "code": 0xA1, "parameters": ""])
           let fram = try await readBlocks(0, 43)              // 3 at a time, like the real reader
           let cmd = /* same bytes as SensorPairingService.nfcCommand(.enableStreaming, 42, …) */
           let mac = try await op(["op": "custom", "code": 0xA1, "parameters": cmd.hex])
           guard sensorUID.count == 8, patchInfo.count == 6, fram.count == 344,
                 SensorType(patchInfo: patchInfo) == .libre2 else { throw PairingError.noSensorData }
           let plain = try Libre2.decryptFRAM(type: .libre2, id: [UInt8](sensorUID), info: patchInfo, data: [UInt8](fram))
           return SensorPairingInfo(uuid: sensorUID, patchInfo: patchInfo, fram: Data(plain),
                                    streamingEnabled: mac.count == 6,
                                    macAddress: mac.count == 6 ? Data(mac.reversed()).hexEncodedString().uppercased() : nil)
       }
   }
   #endif
   ```

   To build the 0x1E parameters, factor `nfcCommand` out of `SensorPairingService` into an `internal`
   static function that both services call, so they can't drift apart.

2. `LibreTransmitterManagerV3.pairingService`: return `SimulatedSensorPairingService()` under
   `#if targetEnvironment(simulator)`.
3. `Features.phoneNFCAvailable`: return `true` under `#if targetEnvironment(simulator)`.
4. Add both new files to `LibreTransmitter.xcodeproj` (LibreTransmitter target).

**Done when** Loop (LoopWorkspace, Simulator) → add Libre → "Libre 2 direct" → scan completes against
libre2sim, and the settings screen shows the sim's serial and wear time. BLE can still be stubbed at
this point.

---

## Milestone 4: BLE transport + streaming (1½ days)

`impossible.go` changes:

```go
var simServices = []gattService{
    {"FDE3", []gattChar{
        {"F001", propWrite, libre2.CharWrite},
        {"F002", propNotify, libre2.CharNotify},
    }},
}
// discovered(): kCBAdvDataLocalName = AdvertisedName() (MAC hex, no colons),
//               kCBAdvDataServiceUUIDs = ["FDE3"]
```

`peripheralID` is derived from `"libre2sim " + name`. Advertise only while the sensor is activated,
`StreamingCode != nil`, it isn't connected and it isn't in an outage.

`libre2/handler.go` (per link):

| Event | Action |
|---|---|
| subscribe F002 | remember it |
| write F001, 12 bytes | `time = LE32(b[0:4])`, `count = time − *StreamingCode`. Check `b[4:12] == unlockPayload(uid, info, *StreamingCode, count)[4:]`. In strict mode (`-strict-unlock`) also require `count > UnlockCount`. Bad unlock → hang up (sim's choice). Good → `UnlockCount = count`, streaming = true, send a packet now. |
| each new life-count minute (1 s ticker) | if streaming and subscribed, `notifyPacket()` |
| 60 s with no valid unlock | hang up (libre3sim's idle rule) |

`libre2/packet.go` (builder):

```go
func (s *Sensor) packetPlain(lc int) []byte // 44 bytes
```

- 7 trend records at `lc − {0,2,4,6,7,12,15}`, 3 history records at `((lc−2)/15)*15 − 15k`.
- Each record is 4 bytes: raw (14 bits) | rawTemp>>2 (12) | tempAdj>>2 (5) | sign (1).
- `age = lc` LE at bytes 40–41, `libreCRC16(pt[0:42])` BE at bytes 42–43.
- Warmup or missing minute → raw = 0.

`notifyPacket`: `n0, n1` from a per-sensor counter, `encryptPacket`, send as **20 + 20 + 6** notifies
(they must add up to exactly 46).

`libre2/glucose.go`: the inverse of `GlucoseFromRaw.glucoseValueFromRaw`:

```go
func (c Calibration) temperatureC() float64             // from rawTemp, tempAdj, i6 (constants from the Swift file)
func (c Calibration) rawFor(mgdl float64) uint16        // closed-form inverse, rounded, held to [1, 16383]
func (c Calibration) mgdlFor(raw uint16) float64        // forward port, for tests
```

Port the `t1`/`t2` tables from `GlucoseFromRaw.swift` (index `i2 − 1`).

**Tests:**

- `decryptPacket(notify bytes)` → CRC OK → a Go port of `parseBLEData` gives the right age, and
  `mgdlFor(raw)` rounds to the modelled glucose for every value in 40…400.
- Handler: no unlock → no notifies. Correct unlock → packet. Replayed count in strict mode → hang up.
  Out of range → hang up and stop advertising.
- `impossible_test.go`, adapted: scan sees name = MAC and service FDE3. Connect → discover → notify →
  write → `didUpdateValue` ×3.

**Done when** Loop shows a new reading every 5 minutes that matches `/status` within ±1 mg/dL once
the 5-point smoothing has settled (≈ 25 min of constant `fixed` glucose). The trend arrow follows a
`sine` mode.

---

## Milestone 5: Control API, lifecycle, faults (1 day)

`api.go`, port 8081 by default:

| Endpoint | Behaviour |
|---|---|
| `GET /status` | uid, serial, patchInfo, MAC/name, lifeCount, wear, state, streamingCode, unlockCount, connected, advertising, glucose |
| `POST /nfc` | `{"op":…}` as in libre2-plan.md §2; 422 `{error, code}` on an NFC error |
| `POST /glucose`, `/disconnect`, `/out-of-range`, `/new` | as in libre3sim |
| `POST /activate` | start a not-yet-started sensor (stands in for the Abbott app, which LibreTransmitter can't replace) |
| `POST /terminate` | end the sensor: FRAM state 05, streaming stops, the link drops |
| `POST /fault` | `{"kind":"badcrc"}` corrupts the next packet's CRC; `{"kind":"short"}` sends 45 bytes |

Flags: `-fresh`, `-age`, `-days 15`, `-warmup 60m`, `-state`, `-api :8081`,
`-socket /tmp/impossible-libre2.sock`, `-strict-unlock`, plus `-simulator`, `-relaunch`, `-suspend`
from libre3sim.

**Scenario checklist (manual, in Loop):**

1. Fresh `-age 3h` sensor: pair → readings.
2. Kill and relaunch Loop: it reconnects through `preSelectedDevice` with no rescan, and the unlock
   count goes up.
3. `/out-of-range 20`: readings gap, then resume. LibreTransmitter does no BLE backfill, so the gap
   stays. That's expected.
4. `/glucose fixed 55`: Loop's low alert fires.
5. Near the end of wear (`-age 14d23h`): expiry alerts. Past the end: no packets.
6. `/fault badcrc`: Loop shows a checksum error and recovers on the next packet.
7. `/new` then rescan: the new UID makes the old calibration mapping stale until the re-pair, after
   which readings resume.
8. Run beside libre3sim through `impossible-mux`, one app with both plugins.

**Done when** every item passes and the results are noted in the README.

---

## Milestone 6: Docs and release (½ day)

- README: disclaimer, run steps (`go build`, `build-impossible.sh`, `SIMCTL_CHILD_*` env with
  `LIBRE2SIM_URL`), API table, the "what it emulates" table with sim's-choice notes, and credits
  (LibreTransmitter/LibreDirect/xDrip for the protocol, g7sim/libre3sim for the transport,
  ImpossiBLE).
- Open the LibreTransmitter PR (M3) with a link to libre2sim, plus a separate small issue for the
  trend-timestamp oddity.
- Licence check of the ported algorithm and crypto code before publishing (libre2-plan.md §6).

---

## Order, dependencies, estimate

```
M0 ─► M1 ─► M2 ─┬─► M4 ─► M5 ─► M6
                └─► M3 ─┘   (M3 needs M2's /nfc; M4's end-to-end check needs M3)
```

About 7 working days. M1 carries the most risk: if the Go crypto port doesn't reproduce the Swift
vectors exactly, nothing downstream works, which is why the Swift golden-vector harness comes first.

## Open items to settle during the work

- [ ] UID byte order of `PreLibre2` examples vs `tag.identifier` (settle it in M1 test 1).
- [ ] Real Libre 2+ patchInfo byte 0 and wear minutes, if a dump turns up. Update the defaults then.
- [ ] Whether ImpossiBLE forwards `kCBAdvDataManufacturerData`. Only needed to also test the legacy
      `ABBOTT…` match path.
- [ ] Strict vs lenient unlock as the default (start lenient).
