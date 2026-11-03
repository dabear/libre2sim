# devicecheck

Shows that the LibreTransmitter integration patch changes nothing for people running LibreTransmitter on a phone.

```bash
tools/devicecheck/devicecheck.sh <LibreTransmitter @4110512> <the same with the patch applied> <scratch dir>
```

1. **Source, as a device build sees it.** `device_view.py` resolves every `#if targetEnvironment(simulator)` block to
   its `#else` side (or nothing) and the script diffs each patched file against the original. Expected: only
   `private` → `fileprivate` on `SensorPairingService.nfcCommand`, and a blank line.
2. **The compiled code.** The pairing code (`LibreSensor/SensorPairing/*.swift`, `Features.swift` and what they need) is
   compiled for arm64 iOS with `-O -wmo`, from the same path both times, and the object files are compared byte by byte.
   Expected: `16 bytes in __LLVM,__swift_modhash` only. That section is a hash of the module's source text, which the
   build system uses; it is not code or data. Machine code, data, symbols and section sizes are identical.

`LibreTransmitterManagerV3.swift` imports LoopKit, so it is not compiled here. Step 1 shows it is token for token the
same on a device.
