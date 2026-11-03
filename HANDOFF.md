# libre2sim — orchestration handoff

> **Rules for whoever picks this up (human or agent). These are mandatory:**
> 1. Read this whole file before doing anything.
> 2. **Update this file after every step**: when you start a task, when a subagent reports back, after each
>    verification, and before you stop. Then **commit it right away** (`git add HANDOFF.md && git commit`).
>    The user's explicit requirement is that nothing gets lost if the session ends suddenly, e.g. when
>    weekly usage limits are hit.
> 3. Add a line to the **Session log** at the bottom for each session: date, what was done, where you
>    stopped.
> 4. When you delegate to a subagent, tell it **not** to edit HANDOFF.md and not to commit. The
>    orchestrator verifies the work, commits it, then updates this file.
> 5. Don't post anything outside this machine (GitHub issues/PRs, pushes) without the user's explicit OK.

**Last updated:** 2026-11-03 (session 2, start). **State:** M0–M6 are done, verified and committed. **Session 2 task
(user: "finish implementation and notes on how to run it"):** run scenarios 3, 4, 5, 7 with cbharness (open decision 2,
now approved by that request), fix what they find, and write step-by-step run notes (`docs/running.md`). Full Loop
build (decision 1) and upstream posts (3) are still not approved.

---

## 1. What this project is

libre2sim is a Go emulator of a **FreeStyle Libre 2 Plus (EU)** sensor. It lets
[LoopKit/LibreTransmitter](https://github.com/LoopKit/LibreTransmitter) (a Loop CGM plugin) pair and stream inside
the **iOS Simulator** with no real sensor. It follows the design of [LoopKit/libre3sim](https://github.com/LoopKit/libre3sim):

- **NFC:** the Simulator has no NFC. A small LibreTransmitter patch (`integration/LibreTransmitter/`) makes
  pairing call libre2sim's HTTP `POST /nfc` instead of CoreNFC.
- **BLE:** the app's CoreBluetooth is redirected by the injected [ImpossiBLE](https://github.com/mickeyl/ImpossiBLE) dylib
  to a Unix socket, where libre2sim plays the sensor (`impossible.go`).
- **Spec:** LibreTransmitter's own Swift is the spec. Everything is verified against it (see §6).

Repo: `/Users/bjorninge/Code/libre2sim` (local git only; **no remote yet**).

## 2. Documents in the repo

| File | Purpose |
|---|---|
| `HANDOFF.md` | This file |
| `CLAUDE.md` | Tells any Claude session here to read and update HANDOFF.md |
| `README.md` | User-facing docs: what it emulates, how to run with Loop, flags, API, verification, credits |
| `libre3sim.reference.md` | How libre3sim works (the model for this project) |
| `libre2-plan.md` | What LibreTransmitter expects from a Libre 2 (protocol notes) |
| `libre2-implementation-plan.md` | Milestones M0–M6: files, signatures, tests, "done when" |
| `docs/scenarios.md` | 8 manual scenarios with curl commands and expected results; verification marks per scenario |
| `docs/libre3sim-transport-fixes.md` | Two transport bugs found in libre3sim, with diffs (for upstreaming) |
| `integration/LibreTransmitter/0001-Pair-with-libre2sim-in-the-iOS-Simulator.patch` | The LibreTransmitter patch (against `4110512`); minimal 3-file version since session 2 (finding 23) |
| `integration/LibreTransmitter/PR.md` | Ready-to-paste PR description for that patch |

## 3. Code map

| Path | What |
|---|---|
| `main.go`, `api.go`, `web.go` | Flags, HTTP API (`/status /nfc /glucose /activate /new /disconnect /out-of-range /terminate /fault /force-quit`), control panel at `/` |
| `impossible.go` | ImpossiBLE provider: GATT FDE3 (F001 write, F002 notify), advert name = MAC hex, iOS restoration/relaunch/suspend logic (from libre3sim, with 2 fixes) |
| `libre2/crypto.go crc.go unlock.go` | Bit-exact port of LibreTransmitter `PreLibre2.swift` / `CRC.swift` |
| `libre2/fram.go` | 344-byte FRAM built from the Example2 template; encryption |
| `libre2/packet.go` | 46-byte BLE packet (7 trend + 3 history records), encryption, 20+20+6 notifies, one-shot faults |
| `libre2/glucose.go glucose_tables.go` | Forward and **exact inverse** of LibreTransmitter's `GlucoseFromRaw` |
| `libre2/state.go sensor.go nfc.go ble.go handler.go serial.go client.go` | Sensor model, NFC commands, BLE link handler (unlock check, streaming) |
| `suspend/`, `simulator/` | From libre3sim: app suspension, and `build-impossible.sh` + 8 ImpossiBLE patches (0008 is ours, finding 19) |
| `ref/libre3sim/*.go.txt` | Verbatim libre3sim sources kept for reference (not compiled) |
| `tools/swiftvectors/` | Swift program printing golden vectors from verbatim LibreTransmitter sources → `testdata/swift_vectors.json` |
| `tools/pairharness/` | iOS-Simulator exe: LibreTransmitter's real pairing code vs a live libre2sim |
| `tools/bleharness/` | iOS-Simulator exe: LibreTransmitter's real crypto/parse, speaking the ImpossiBLE socket protocol directly (no dylib) |
| `tools/cbharness/` | iOS-Simulator exe: **real CoreBluetooth + real ImpossiBLE dylib + LibreTransmitter's real `Libre2DirectTransmitter`**. Modes `PACKETS`, `RECONNECT=1`, `REPLAY=n` |

## 4. Milestone status

| M | What | Status | Commit |
|---|---|---|---|
| M0 | Scaffold | ✅ verified | `aa09a72` |
| M1 | Crypto + CRC port, Swift golden vectors | ✅ verified (finding 1) | `be5ec61` |
| M2 | State, FRAM builder, glucose inverse, NFC model, `/nfc` API | ✅ verified live (finding 8) | `f3a79e7` |
| M3 | LibreTransmitter Simulator hook (patch) | ✅ verified (findings 6, 8) | `0b3be5d` |
| M4 | BLE transport + streaming | ✅ verified with real CoreBluetooth/ImpossiBLE/plugin (findings 11–15) | `4df02ef` |
| M5 | Faults, control panel, API tests, scenarios doc | ✅ verified (finding 16) | `9f3e7a5` |
| M6 | README, PR text, libre3sim fixes doc | ✅ reviewed; the badcrc claim was corrected | `9f3e7a5` |
| Harnesses | pairharness `f233351`/`f3a79e7`, bleharness `0cf3870`, cbharness `6167c74` + modes `7284548` | ✅ | |

`GO111MODULE=on go test -race ./...` passes (packages `libre2sim`, `libre2sim/libre2`, `libre2sim/suspend`).

## 5. Environment gotchas (this machine)

- Go **1.20.3**: no `min`/`max` builtins, `slices`, `maps` or `log/slog`.
- **`GO111MODULE=off` is set globally.** Always prefix go commands with `GO111MODULE=on`.
- Xcode 27.2 / Swift 6.4.2. LibreTransmitter sources need `-swift-version 5` (6.x times out on an `NSRange`
  expression). `Sensor.swift` lacks `import Foundation` (Xcode tolerates it), so swiftc builds that include it
  need `-Xfrontend -import-module -Xfrontend Foundation`.
- **macOS Unix socket paths must be under 104 bytes.** Scratchpad paths are too long, so use
  `/tmp/impossible-libre2.sock` or a relative path.
- The **auto-mode permission classifier** blocks running code from downloaded repos ("Code from External"),
  e.g. libre3sim's tests or `simulator/build-impossible.sh`. The user approved the ImpossiBLE build once in
  session 1. In a new session, **ask the user again**, or have them run `! sh simulator/build-impossible.sh <outdir>`.
- Leftover state at the end of session 1: the iPhone 16 simulator `3B79BF91-28E9-4404-BC1E-828BB4ABF0C3` is
  **still booted** (`xcrun simctl shutdown 3B79BF91-28E9-4404-BC1E-828BB4ABF0C3` to stop it), and a stale
  `/tmp/impossible-libre2.sock` remains (harmless: libre2sim replaces it when nothing is listening). No
  libre2sim processes are running.

## 6. Verified findings (not obvious from the code; keep them)

1. **The Go crypto is bit-exact with LibreTransmitter's Swift.** `tools/swiftvectors` compiles verbatim copies of
   `PreLibre2.swift`/`CRC.swift` and calls the real `Libre2.streamingUnlockPayload`, `decryptFRAM`, `decryptBLE` and
   `nfcCommand`. A rebuild reproduced `testdata/swift_vectors.json` byte for byte.
2. **The xDrip FRAM sample in `FakeSensorPairingData.swift` is inconsistent.** It fails all three CRCs with its own
   patch info, in LibreTransmitter's Swift too. It would only decrypt with `patchInfo[4..5] = 13 09` instead of
   `9c 16`. **The FRAM template is `Example2`** (uid `df20be0000a407e0`, info `9d0830017625`), whose CRCs are valid.
3. **There is only one CRC16.** `Crc.crc16(seed: 0xffff)` equals PreLibre2's private `crc16`; the unlock payload
   byte-swaps the result once more.
4. `BLEExample` decrypts with a valid CRC (age 11509 min).
5. In `decryptBLE`, `x = (d1d0 ^ d3d2) | 0x63`: Swift's `^` and `|` share precedence and associate left.
6. **M3 patch:** `Libre2NFC.enableStreamingCommand` produces the same bytes as the removed `nfcCommand`. The patch
   typechecks with 0 errors for the iOS Simulator and device. The pairing UI only consumes `onCancel`, so errors
   appear only in the console.
7. LibreTransmitter quirks (not ours): `parseBLEData` stamps trend record *i* at `now − i min` (wrong for records 1–6);
   its README says `GetGlucoseFromRaw.swift` is missing, but the file is in the repo.
8. **M2 live:** the real `SimulatedSensorPairingService` (pairharness) paired with `libre2sim -fresh -age 3h`. FRAM CRCs
   are valid, state is ready, minutes 180, wear 21600, MAC == advertisedName, serial and calibration == `/status`, and the
   FRAM trend decoded through **LibreTransmitter's own GlucoseFromRaw** to exactly the modelled 80 mg/dL.
9. **The sim's calibration** comes from Example2: i1=0, i2=898, i3=24, i4=6819, i5=8904, i6=6192. Temperatures come from
   BLEExample record 0 (rawTemp 6336, adj +8, ≈31.6 °C). For 40…400 mg/dL, raw runs 624…4670, strictly increasing.
10. **M2 sim's choices:** patchInfo `C6 09 31 01 rr rr`; FRAM state 01/02/03, and 05 for both expired and terminated
    (minutes frozen); trend slot (m−1) mod 16, body[2] = lc mod 16; history minute 15j in slot (j−1) mod 32, body[3] =
    ((lc−3)/15) mod 32; one FRAM image per scan, cached 30 s; NFC errors 0x10/0x01/0x02 (bad d, checked first)/0xB0
    (not activated or ended; warmup OK); re-enabling streaming resets UnlockCount; a glucose mode change affects only
    future minutes.
11. **M4, bleharness:** 2 consecutive packets as 20+20+6, CRC valid, age == lifeCount, trend and history == the sine model.
12. **M4, cbharness (real CoreBluetooth + real ImpossiBLE + real `Libre2DirectTransmitter`):** scan [FDE3] → name == MAC,
    `canSupportPeripheral` true → connect → F001 props 8, F002 props 16 → the plugin writes its own unlock (count 1) →
    2 packets through the plugin's 46-byte reassembly, CRC true, glucose == model.
13. **Two libre3sim transport bugs, fixed in M4** (see `docs/libre3sim-transport-fixes.md`): the held-app relaunch could
    never fire (`c == nil` tested inside `c != nil`), and a link could be published before its handler existed.
14. **M4 sim's choices:** it advertises only when activated, not ended, streaming enabled over NFC, no link held and in
    range; any bad unlock → hang up; `-strict-unlock` (opt-in) refuses a count ≤ the last accepted; no unlock in 60 s →
    hang up; packets stop on terminate or expiry; the nonce is a per-launch uint16 counter; F001 is listed before F002,
    so LibreTransmitter writes the unlock before subscribing (both orders work).
15. **Reconnect and strict unlock (cbharness, `-strict-unlock`):** counts 1→2→3 across reconnects were accepted.
    `REPLAY=2` was refused ("unlock count 2 replayed: the last accepted was 3"): the app got didDisconnect and the
    sensor re-advertised.
16. **LibreTransmitter fault behaviour (cbharness):**
    - `badcrc`: the packet is **dropped silently**. `decryptBLE` throws into an empty `catch {}` in
      `Libre2DirectTransmitter.handleCompleteMessage`, so **`checksumValidationError` never fires** for a corrupted CRC.
      The next minute parses fine.
    - `short` (45 bytes): **LibreTransmitter stalls until it reconnects.** It parses only at exactly 46 bytes and resets
      the buffer only on notify-state change or reconnect, so all later packets pile up unparsed. This is a real
      robustness bug (one lost notify on a real link does the same) and is worth an upstream issue.
17. The M5 subagent made `/glucose` return 400 for unknown kinds; before, they silently fell back to sine.
18. **Session 2, scenario 4 (cbharness):** `fixed 55` → minute 181 decodes to 55 through the real plugin; earlier minutes
    stay at the old model (future minutes only), as designed.
19. **ImpossiBLE bug, found by scenario 3, fixed by `simulator/impossible-patches/0008`:** ImpossiBLE removed
    `gPeripheralOwners[uuid]` *after* calling the app's `didDisconnectPeripheral`. LibreTransmitter reconnects from inside
    that callback (`requiresDelayedReconnect` false), so `connect` re-registered the owner and ImpossiBLE then deleted it.
    `didConnect` still arrived (falls back to `gCentral`), but `didDiscoverServices` needs the owner and was dropped
    silently → no characteristics, no unlock → libre2sim's 60 s unlock timeout hung up, forever. **Any drop in Loop
    (`/disconnect`, out of range, terminate-reconnect) hit this before.** Patch 0008 removes the owner before calling the delegate.
20. **Scenario 3 (cbharness `RECONNECT=1 FOLLOW=1`, with patch 0008):** out of range 2 min → didDisconnect, the app's
    connect is held, no advertising for 2 min, then advertise → connect completes → unlock count 4 → minute 193 decoded.
    cbharness got `FOLLOW=1` (reconnect in didDisconnect, as the proxy manager does) and `TIMEOUT=s`.
21. **Scenario 5 (cbharness):** terminate mid-stream → hang-up, no further packets, the app's reconnect is held forever,
    FRAM 05 with lifeCount frozen; a new pairing fails with NFC 0xB0 (LibreTransmitter logs it, UI shows nothing; finding 6).
    Expiry (`-age 359h58m`): packets 21598, 21599, then at 21600 hang-up "the sensor has ended", no packet 21600.
    Note: cbharness stdout to a file is buffered, so "PACKET n" can appear in the file a little late.
22. **Scenario 7 (cbharness):** `/new` (age 180) → new UID/serial/MAC, not advertising until NFC enables streaming;
    `RECONNECT=1` with the old sensor finds nothing (timeout); a fresh pairing gets the new MAC and streams.
23. **Minimal LibreTransmitter patch (session 2, replaces the 6-file one):** 3 files, +160/−1, no new file, no pbxproj, no
    refactor. `SimulatedSensorPairingService` lives at the end of `SensorPairingService.swift` under `#if targetEnvironment(simulator)`
    and calls the real `SensorPairingService().nfcCommand(.enableStreaming, …)` (`private` → `fileprivate`, the only edit to
    existing code); plus the 4-line hooks in `Features.swift` and `LibreTransmitterManagerV3.swift`. Verified: Simulator build +
    **device typecheck (exit 0)**, a negative control (device build can't see the simulated class), and pairharness,
    bleharness and cbharness all `RESULT: OK` live. bleharness now builds the enable command itself from the real
    `Libre2.usefulFunction` (Libre2NFC is gone); libre2sim's `d` check accepted it.
24. **Control panel v2 (Sonnet subagent, verified):** current + next reading with countdown, 60-min SVG chart, event log
    (`GET /events?since=`, 200-entry ring in `libre2/events.go`, NFC hex in/out, BLE, sensor), `GET /readings`, `GET/POST /config`
    (strictUnlock is the only live-editable setting; `/new` now carries its current value). `/status` only gained fields.
    Next reading: exact for fixed/sine (`model(lc+1)`), the mean for walk (`estimate: true`, never draws randomness).
    Orchestrator check: gofmt/vet/`test -race` clean; diff read; live with cbharness: `/status` predicted minute 181 = 80 mg/dL,
    the real plugin decoded 181:80; `/events` held the whole NFC exchange, enable-streaming, connect, unlock, 2 packets,
    disconnect. The page was **not** looked at in a browser (only that it serves and has no external assets).
25. **The patch doesn't change device builds (user asked):** `tools/devicecheck/devicecheck.sh`. Device-side source view (`#if
    simulator` resolved) differs only by `private`→`fileprivate` + a blank line; arm64 iOS `-O -wmo` objects of the pairing code
    differ in 16 bytes, **all in `__LLVM,__swift_modhash`** (source-text hash, not code). ManagerV3 can't compile here (LoopKit), but
    its device view is identical. LibreTransmitter's own unit tests were not run (need LoopKit / a full workspace).

## 7. Scenario verification (`docs/scenarios.md`)

| # | Scenario | cbharness | In Loop |
|---|---|---|---|
| 1 | Pair + first readings | ✅ | not run |
| 2 | Reconnect without rescan / strict unlock | ✅ | not run |
| 3 | Out of range | ✅ (finding 20, needed patch 0008) | not run |
| 4 | Low glucose (e.g. fixed 55) | ✅ (finding 18) | not run |
| 5 | Expiry / terminate | ✅ (finding 21) | not run |
| 6 | Faults (badcrc, short) | ✅ (finding 16) | not run |
| 7 | New sensor | ✅ (finding 22) | not run |
| 8 | Beside libre3sim via impossible-mux | n/a | not run |

Scenarios 3, 4, 5 and 7 can be done with cbharness without asking the user (cheap, local). Update this table and the
marks in `docs/scenarios.md` when you run them.

## 8. Open decisions (asked of the user at the end of session 1; no answer yet)

1. **Full Loop build in the Simulator** (clone LoopWorkspace, apply the patch to its LibreTransmitter, build, launch with
   the dylib injected). Large clone and long build. **Ask before starting.**
2. **Run scenarios 3, 4, 5 and 7 with cbharness.** Offered as cheap; not yet approved or started.
3. **Upstream posts:** the LibreTransmitter PR (`integration/LibreTransmitter/PR.md` + patch); a LibreTransmitter issue
   for finding 16 (short-packet stall, silent CRC drop) and finding 7 (trend timestamps); the libre3sim transport fixes.
   **Nothing may be posted without the user's OK.**
4. **Publishing libre2sim** (there's no remote; README and PR.md have no libre2sim URL yet).

## 9. How to recreate the scratch setup (session 1's scratchpad is gone)

Use your own session scratchpad (`$S` below). Keep socket paths short (see §5).

```bash
# Reference repos (read-only)
git clone https://github.com/LoopKit/LibreTransmitter $S/LibreTransmitter && git -C $S/LibreTransmitter checkout 4110512
git clone https://github.com/LoopKit/libre3sim $S/libre3sim            # ccd9a91
git clone https://github.com/LoopKit/LibreLoop $S/LibreLoop            # 3100b98 (pattern for the NFC hook)
git clone https://github.com/airedev326/RoundWhiteDiscKit $S/RWDK      # 7dd6396 (Libre 3 background only)

# LibreTransmitter with the patch (needed by all harnesses)
cp -R $S/LibreTransmitter $S/LT-work && cd $S/LT-work && git checkout -b simulator-libre2sim \
  && git am /Users/bjorninge/Code/libre2sim/integration/LibreTransmitter/0001-*.patch

# ImpossiBLE dylib (needs the user's OK; see §5)
sh /Users/bjorninge/Code/libre2sim/simulator/build-impossible.sh $S/impbuild   # → $S/impbuild/libImpossiBLE.dylib

# libre2sim
cd /Users/bjorninge/Code/libre2sim && GO111MODULE=on go build -o $S/libre2sim .
$S/libre2sim -fresh -age 3h -api 127.0.0.1:8081 -socket /tmp/impossible-libre2.sock -state $S/state.json &

# Harnesses: build from $S/LT-work. The exact file lists are in each tools/*/README.md
# (cbharness's list is the superset). Then:
xcrun simctl boot "iPhone 16"
SIMCTL_CHILD_DYLD_INSERT_LIBRARIES=$S/impbuild/libImpossiBLE.dylib SIMCTL_CHILD_IMPOSSIBLE_SOCKET=/tmp/impossible-libre2.sock \
  SIMCTL_CHILD_PACKETS=2 xcrun simctl spawn "iPhone 16" $S/cbharness          # expect RESULT: OK
```

Notes: pairharness and bleharness need no dylib. cbharness keeps its stored sensor in the simulator's UserDefaults
between runs (`RECONNECT=1` reuses it). Two packets take about 1 minute (one per sensor minute).

## 10. How the work is run (method)

- The orchestrator delegates each milestone to a subagent: **Opus** for bit-exact protocol work (M1, M2, M4), **Sonnet**
  for Swift glue, API and docs (M3, M5+M6). Briefs point at `libre2-implementation-plan.md`, this file and the exact
  reference paths, and state the environment gotchas.
- The **orchestrator verifies everything itself** before committing: rerun `gofmt`/`vet`/`test -race`, read the diff,
  and run an **independent** check through LibreTransmitter's real Swift (the harnesses). Never commit unverified
  subagent work. Session 1 caught one wrong claim this way (the badcrc doc).
- Commits end with `Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>`. Commit only the paths you mean to.
  Subagents may leave work-in-progress in the tree.

## 11. Session log

| Date | Session | What happened | Stopped at |
|---|---|---|---|
| 2026-11-03 | 1 | Analysed libre3sim/LibreLoop/RoundWhiteDiscKit/LibreTransmitter; wrote the plans; ran M0–M6 with subagents (M2's first attempt was stopped by the user and restarted); built the 4 harnesses; verified the full BLE path with real CoreBluetooth + ImpossiBLE + LibreTransmitter's plugin; found the LibreTransmitter fault bugs (finding 16). | The user said stop. Everything is committed. Open decisions in §8 are unanswered. |
| 2026-11-03 | 2 | Started: scenarios 3/4/5/7 via cbharness + run notes. Scenarios 4, 3 pass; found+fixed ImpossiBLE owner bug (0008). Scenario 5 passes. User asked mid-session: (a) a better control panel (current/next reading, NFC/event log, config): **delegated to a Sonnet subagent** (web.go/api.go/libre2, README API section; told not to touch tools/integration/simulator/docs/scenarios.md); (b) 'what would it take to pair with LibreTransmitter, change as little as possible': plan is to shrink the patch to 3 files (no refactor/new file/pbxproj). | in progress: scenario 7, patch shrink, UI subagent running |
