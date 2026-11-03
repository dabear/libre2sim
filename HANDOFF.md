# libre2sim — orchestration handoff

Read this first if you are resuming the work. It records where things stand, what has been verified,
and how to continue. Keep it updated after every milestone.

**Last updated:** 2026-11-03, during M2.

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
| M3 | LibreTransmitter Simulator hook: `integration/LibreTransmitter/0001-Pair-with-libre2sim-in-the-iOS-Simulator.patch` | ✅ done, statically verified (not yet run against a live server) | `0b3be5d` |
| M2 | Sensor state, FRAM builder, glucose inverse, NFC model, `main.go`/`api.go` with `/nfc` `/status` `/glucose` `/activate` `/new` | 🔄 in progress (subagent, Opus). First attempt was stopped by the user before it wrote anything; restarted with the same brief | — |
| M4 | BLE: adapt `ref/libre3sim/impossible.go.txt` (service FDE3, F001 write, F002 notify, advert name = MAC), handler (unlock check → 46-byte packet each minute as 20+20+6), packet builder | ⏳ todo | — |
| M5 | Control API + faults (`/terminate`, `/out-of-range`, `/disconnect`, `/fault`), scenario checklist in Loop | ⏳ todo | — |
| M6 | README, credits, LibreTransmitter PR text | ⏳ todo | — |

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

## Independent check harness (orchestrator-owned)

`tools/pairharness/` builds LibreTransmitter's **real** pairing code (with the M3 patch) as an
iOS-Simulator executable and pairs against a live libre2sim. See its README. Status: it builds and
runs in the simulator, and fails cleanly when no server is running. **Next step:** run it against
M2's server and check `RESULT: paired`, valid CRCs, `state` = ready, wear = 21600, a MAC equal to the
advertised name, and sane calibration.

## How to resume

1. `cd /Users/bjorninge/Code/libre2sim && git log --oneline && git status`. Uncommitted files in
   `libre2/` or at the top level are probably a finished or interrupted M2: run
   `GO111MODULE=on go test ./...` and review them before deciding to keep or redo.
2. If M2 is incomplete, re-delegate it using the "Milestone 2" section of
   `libre2-implementation-plan.md` together with findings 2 and 3 above.
3. Verify M2: run the tests, then build and start the server
   (`GO111MODULE=on go build -o /tmp/libre2sim . && /tmp/libre2sim -fresh -age 3h -api 127.0.0.1:8081`),
   then run the pair harness. Commit.
4. Delegate M4 (Opus). Verify by decrypting the notified packets with the Swift `Libre2.decryptBLE` and
   `parseBLEData` (extend `tools/swiftvectors` or the harness) and checking that
   `mgdlForRaw(raw)` ≈ the modelled glucose. Then M5 and M6.
5. A full Loop build in the Simulator (LoopWorkspace + ImpossiBLE) is the final end-to-end check. It
   has not been attempted yet.
