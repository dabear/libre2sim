# libre2sim — orchestration handoff

Read this first if you are resuming the work. It records where things stand, what has been verified,
and how to continue. Keep it updated after every milestone.

**Last updated:** 2026-11-03. M0–M6 are done and verified with the harnesses. What's left: scenarios that need Loop, a full Loop build, and publishing.

## Documents

| File | Purpose |
|---|---|
| `libre3sim.reference.md` | How LoopKit/libre3sim works (the model for this project) |
| `libre2-plan.md` | What LibreTransmitter expects from a Libre 2 sensor (protocol notes) |
| `libre2-implementation-plan.md` | Milestones M0–M6 with files, signatures, tests and "done when" |
| `HANDOFF.md` | This file: status, verified findings, how to resume |

## How the work is run

- An orchestrator (the main Claude session) delegates each milestone to a subagent. Opus is used for
  bit-exact protocol work (M1, M2, M4); Sonnet for Swift glue and API/docs work (M3, M5, M6).
- The orchestrator **verifies every result itself** before committing: rerun the tests, read the
  diff, and do independent checks (see "Verification done" below). Subagents do not commit.
- Commits end with `Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>`.

## Environment gotchas

- Go is **1.20.3**: no `min`/`max` builtins, `slices`, `maps` or `log/slog`. `go.mod` says `go 1.20`.
- **`GO111MODULE=off` is set globally** on this machine. Always run `GO111MODULE=on go test ./...`
  (and `go vet`, `go build`).
- Xcode 27.2 / Swift 6.4.2. LibreTransmitter needs `-swift-version 5`, because 6.x times out
  type-checking an existing `NSRange` expression in `SensorPairingService.swift`.
- The auto-mode permission classifier blocks **running code from downloaded repos** (e.g. libre3sim's
  tests). Read them, copy what we need, and run only our own code.
- An iPhone 16 simulator (`3B79BF91-28E9-4404-BC1E-828BB4ABF0C3`) was booted for the pair harness.

## Reference repos (read-only; re-clone if the scratchpad is gone)

| Repo | Commit | Use |
|---|---|---|
| https://github.com/LoopKit/LibreTransmitter | `4110512` | **The client/spec.** `LibreSensor/SensorContents/PreLibre2.swift` (crypto), `SensorData.swift` (FRAM), `SensorPairing/SensorPairingService.swift` (NFC), `Bluetooth/Transmitter/Libre2DirectTransmitter.swift` + `LibreTransmitterProxyManager.swift` (BLE), `GlucoseAlgorithm/GlucoseFromRaw.swift` |
| https://github.com/LoopKit/libre3sim | `ccd9a91` | Architecture and transport to copy. Its Go files are also kept in `ref/libre3sim/*.go.txt` |
| https://github.com/LoopKit/LibreLoop | `3100b98` | Pattern for the Simulator NFC hook (`LibreLoop/Pairing/LibreLoopSimulator.swift`) |
| https://github.com/airedev326/RoundWhiteDiscKit | `7dd6396` | Libre 3 client (background only) |

The previous session cloned these into its scratchpad directory, which is session-specific. A new
session should re-clone them (shallow is fine) into its own scratchpad.

## Milestone status

| M | What | Status | Commit |
|---|---|---|---|
| M0 | Scaffold: module, `libre2/link.go`, `suspend/`, `simulator/` (ImpossiBLE build + patches), `ref/libre3sim/` | ✅ done, verified | `aa09a72` |
| M1 | Crypto + CRC port (`libre2/crypto.go crc.go fram.go packet.go unlock.go`) + Swift golden vectors (`tools/swiftvectors`, `testdata/swift_vectors.json`) | ✅ done, verified | `be5ec61` |
| M3 | LibreTransmitter Simulator hook: `integration/LibreTransmitter/0001-Pair-with-libre2sim-in-the-iOS-Simulator.patch` | ✅ done, verified live through the pair harness | `0b3be5d` |
| M2 | Sensor state, FRAM builder, glucose inverse, NFC model, `main.go`/`api.go` with `/nfc` `/status` `/glucose` `/activate` `/new` | ✅ done, verified live (see finding 8) | `f3a79e7` |
| M4 | BLE: `impossible.go` (FDE3/F001/F002, advert name = MAC), `libre2/handler.go` (unlock check → 46-byte packet each minute as 20+20+6), packet builder, /disconnect /out-of-range /terminate /force-quit | ✅ done, verified (findings 11–13) | `4df02ef` |
| M5 | `/fault` (badcrc, short), web control panel (`web.go`), API test coverage, `docs/scenarios.md` | ✅ done, faults verified with cbharness (finding 16) | see git log |
| M6 | README rewrite, `integration/LibreTransmitter/PR.md`, `docs/libre3sim-transport-fixes.md` | ✅ done, reviewed; the badcrc claim was corrected by the orchestrator | see git log |

## Verified findings (keep these; they're not obvious from the code)

1. **The Go crypto is bit-exact with LibreTransmitter's Swift.** `tools/swiftvectors` compiles verbatim
   copies of `PreLibre2.swift`/`CRC.swift` and prints vectors. The orchestrator rebuilt it and got
   byte-identical JSON to `testdata/swift_vectors.json`, and checked that `main.swift` calls the real
   `Libre2.streamingUnlockPayload`, `decryptFRAM`, `decryptBLE` and `nfcCommand`.
2. **The xDrip FRAM sample in `FakeSensorPairingData.swift` is inconsistent.** It fails all three CRCs
   with its own patch info, in LibreTransmitter's Swift too. It decrypts only if `patchInfo[4..5]` were
   `13 09` instead of `9c 16`. **Use `Example2`** (`PreLibre2.swift`; uid `df20be0000a407e0`, info
   `9d0830017625`) as the FRAM template: all three CRCs are valid.
3. **There is only one CRC16.** `Crc.crc16(seed: 0xffff)` and the private `crc16` in `PreLibre2.swift` are
   the same function. Inside the unlock payload the caller byte-swaps the result once more.
4. `BLEExample` decrypts with a valid CRC; sensor age 11509 min.
5. In `decryptBLE`, `x = (d1d0 ^ d3d2) | 0x63`: Swift's `^` and `|` share precedence and associate left.
6. **M3 patch:** the refactored `Libre2NFC.enableStreamingCommand` produces the same bytes as the
   removed `nfcCommand`, checked by reading both. The simulated service typechecks with 0 errors for the
   iOS Simulator and device targets, and both new files are registered in the pbxproj. LibreTransmitter's
   pairing UI only consumes `onCancel`, so errors show up only in the console.
7. LibreTransmitter quirks (not ours to fix): `parseBLEData` stamps trend record *i* at `now − i min`
   (wrong for records 1–6); the receive buffer is only parsed at exactly 46 bytes; the README says
   `GetGlucoseFromRaw.swift` is missing, but it's in the repo.

8. **M2 verified live:** LibreTransmitter's real `SimulatedSensorPairingService` (pair harness, iPhone 16
   simulator) paired with `libre2sim -fresh -age 3h`: `RESULT: paired`, FRAM CRCs valid, state ready,
   minutesSinceStart 180, maxMinutesWearTime 21600, MAC == advertisedName, serial and calibration ==
   `/status`, and the FRAM trend decodes **through LibreTransmitter's own GlucoseFromRaw** to exactly the
   modelled value (80 mg/dL = default sine at minute 180). So `rawForGlucose` is proven against the
   real client.
9. **The sim's calibration:** Example2's FRAM gives i1=0, i2=898, i3=24, i4=6819, i5=8904, i6=6192.
   Temperatures come from BLEExample record 0 (rawTemp 6336, adj +8, ≈31.6 °C). For g = 40…400 mg/dL,
   raw runs 624…4670, strictly increasing.
10. **M2 sim's choices** (also commented in the code): patchInfo `C6 09 31 01 rr rr`; FRAM state 01/02/03,
    and 05 for both expired and terminated, with minutes frozen; trend slot (m−1) mod 16 with body[2] = lc mod 16;
    history slot for minute 15j at (j−1) mod 32 with body[3] = ((lc−3)/15) mod 32; one FRAM image per scan,
    cached for 30 s; NFC errors 0x10 (block out of range), 0x01 (unknown), 0x02 (bad d, checked first),
    0xB0 (not activated/ended; warmup allowed); re-enabling streaming resets UnlockCount; changing the
    glucose mode only affects future minutes (as in libre3sim).

11. **M4 verified with bleharness** (LibreTransmitter's Swift crypto/parse over the socket protocol): two
    consecutive packets as 20+20+6, CRC valid, age == lifeCount (180, 181), trend/history == the sine model.
12. **M4 verified with cbharness: real CoreBluetooth + real ImpossiBLE dylib + LibreTransmitter's real
    `Libre2DirectTransmitter`.** Pairing → scan for [FDE3] → didDiscover name == MAC, `canSupportPeripheral` true
    → connect → F001 (props 8) and F002 (props 16) → the plugin writes its own unlock
    (`2b000000…`, count 1) → didWrite OK → notify on → 2 packets via the plugin's 46-byte reassembly, CRC true,
    glucose == model. Remaining unverified: a full Loop build (LoopWorkspace) and the real
    LibreTransmitterProxyManager (it imports LoopKit).
13. **Fixes M4 made to libre3sim's transport** (worth upstreaming to libre3sim): the held-app relaunch could never
    fire (`c == nil` tested inside `c != nil`), and a link could be published before its handler existed. Also:
    **macOS limits Unix socket paths to 104 bytes**, so long scratchpad paths fail with `bind: invalid argument`. Use
    `/tmp/impossible-libre2.sock` or a relative path.
14. M4 sim's choices: it advertises only when activated, not ended, streaming enabled over NFC, no link held,
    and in range; any bad unlock means hang up; strict replay checking is opt-in (`-strict-unlock`); no
    unlock within 60 s means hang up; packets stop on terminate or expiry; the nonce is a per-launch uint16
    counter; F001 is listed before F002, so LibreTransmitter writes the unlock before subscribing, and both
    orders work.

15. **Reconnect and strict unlock, verified with cbharness** against `libre2sim -strict-unlock` built from `6167c74`:
    pair (count 1) → `RECONNECT=1` (count 2) → `RECONNECT=1` (count 3); each got a packet with a valid CRC, and `/status`
    unlockCount = 3. `RECONNECT=1 REPLAY=2` → the sim logged "hanging up: bad unlock: unlock count 2 replayed: the
    last accepted was 3", the app got didDisconnect, and the sensor advertised again.

16. **Fault behaviour in LibreTransmitter, verified with cbharness:**
    - `badcrc`: the packet is **dropped silently**. `decryptBLE` throws, and `Libre2DirectTransmitter.handleCompleteMessage` has
      an empty `catch {}`, so `checksumValidationError` never fires for a corrupted CRC. The next minute parses fine.
    - `short`: **one 45-byte packet stalls LibreTransmitter until it reconnects.** Its buffer is only parsed at exactly 46
      bytes and only reset on notify-state change or reconnect, so every later packet is appended and never parsed.
      This is a real robustness bug in LibreTransmitter (a lost notify on a real link would do the same); worth an upstream issue.
17. The M5 subagent also made `/glucose` reject unknown kinds with 400; before, an unknown kind fell back to sine.

## Remaining work (not started)

- Upstream issues/PRs: the LibreTransmitter PR (`integration/LibreTransmitter/PR.md` + patch); a LibreTransmitter issue for
  finding 16 (short packet stall, silent CRC drop) and finding 7 (trend timestamps); the libre3sim transport fixes
  (`docs/libre3sim-transport-fixes.md`). **Ask the user before posting anything.**
- Scenarios that need Loop in `docs/scenarios.md`: build LoopWorkspace for the Simulator with the patched
  LibreTransmitter, then launch with the dylib injected. Heavy (large clone and build); ask the user first.
- Publishing libre2sim: no remote yet; README/PR.md have no libre2sim URL.

## Independent check harness (orchestrator-owned)

**BLE harness (`tools/bleharness/`, for verifying M4):** a macOS Swift program built from LibreTransmitter's
real `PreLibre2.swift`, `CRC.swift`, `SensorData`, `GlucoseFromRaw` and the M3 `Libre2NFC.swift`. It pairs over
`/nfc`, then speaks the ImpossiBLE JSON socket protocol directly as a central: scan, match name == MAC,
connect, discover, notify F002, write the real `Libre2.streamingUnlockPayload` to F001, reassemble the
46 bytes, and run the real `decryptBLE`, `parseBLEData` and `calibratedGlucose`.

**ImpossiBLE is built.** The user approved running `simulator/build-impossible.sh`. The previous session's dylib was at
`<scratchpad>/impbuild/libImpossiBLE.dylib`; rebuild it in a new session. The classifier blocks it by default
("Code from External"), so ask the user.

**cbharness (`tools/cbharness/`):** real CoreBluetooth with the real ImpossiBLE dylib injected, driving LibreTransmitter's
real `Libre2DirectTransmitter` plugin. See its README.


`tools/pairharness/` builds LibreTransmitter's **real** pairing code (with the M3 patch) as an
iOS-Simulator executable and pairs against a live libre2sim. See its README. Status: passes against M2
(finding 8). It also prints the FRAM trend decoded with LibreTransmitter's own algorithm.

## How to resume

1. `cd /Users/bjorninge/Code/libre2sim && git log --oneline && git status`. Uncommitted files are
   probably a finished or interrupted milestone (see the status table): run
   `GO111MODULE=on go test ./...` and review them before deciding to keep or redo.
2. To run the server: `GO111MODULE=on go build -o /tmp/libre2sim . && /tmp/libre2sim -fresh -age 3h`
   (API on :8081). Then run the pair harness.
3. Delegate M4 (Opus). Verify by decrypting the notified packets with the Swift `Libre2.decryptBLE` and
   `parseBLEData` (extend `tools/swiftvectors` or the harness) and checking that
   `mgdlForRaw(raw)` ≈ the modelled glucose. Then M5 and M6.
4. cbharness scenarios done: 1 (pair + stream), 2 (reconnect/strict), 6 (faults). Still open with cbharness: 3 (out-of-range),
   4 (low glucose decode), 5 (expiry/terminate), 7 (new sensor).
5. A full Loop build in the Simulator (LoopWorkspace + ImpossiBLE) is the final end-to-end check. It
   has not been attempted yet.
