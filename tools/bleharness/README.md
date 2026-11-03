# bleharness

An independent end-to-end check of libre2sim's BLE side. It is built from LibreTransmitter's own Swift:
`PreLibre2.swift` (unlock payload, `decryptBLE`, `parseBLEData`, `usefulFunction`), `SensorData` and `GlucoseFromRaw`.
The enable-streaming command is built from `usefulFunction` as `SensorPairingService.nfcCommand` does (that
function is `fileprivate`).

It pairs over `/nfc`, then acts as an ImpossiBLE client on the provider socket. It scans, matches
the advertised name against the MAC, connects, discovers FDE3/F001/F002, enables F002, writes the
unlock, reassembles 46 bytes, and decodes them. It prints the trend and history in mg/dL as
LibreTransmitter computes them, next to libre2sim's `/status`.

It does **not** go through the real ImpossiBLE dylib: it speaks the socket protocol that libre3sim and
libre2sim implement.

Build: the same command as `tools/pairharness/README.md`, with `$H/main.swift` replaced by
`tools/bleharness/main.swift` and `-o /tmp/bleharness`. Run with:

```bash
SIMCTL_CHILD_IMPOSSIBLE_SOCKET=/tmp/impossible.sock SIMCTL_CHILD_PACKETS=2 \
  xcrun simctl spawn "iPhone 16" /tmp/bleharness
```

Env: `LIBRE2SIM_URL`, `IMPOSSIBLE_SOCKET`, `UNLOCK_COUNT` (default 1), `PACKETS` (default 1).
