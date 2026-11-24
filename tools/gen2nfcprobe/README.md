# gen2nfcprobe

Replays LibreTransmitter's Gen2 NFC pairing (`SensorPairingService+Gen2.swift`, `pairGen2`) against libre2sim in
[Gen2 mode](../../README.md#gen2-mode), one step at a time and in its order, using LibreTransmitter's own request bytes
and parsers (`parseKeyIndex` with `gen2KeyIndexOffset`, `enableStreamingRequest`). Only CoreNFC's `customCommand` is
replaced by a `POST /nfc`, as `SimulatedSensorPairingService` does for Gen1. It reports where a sensor that follows
Juggluco's Gen2 protocol stops the pairing.

Build exactly as [cbharness](../cbharness/README.md) (with the Gen2 files), with this `main.swift` instead of
cbharness's; run it in the iOS Simulator with `SIMCTL_CHILD_LIBRE2SIM_URL` set, Gen2 mode on. No ImpossiBLE needed.

Result against `next-dev+libre2gen2` (f230205), 2026-11-24:

```
patchInfo c6093901181f -> LT securityGeneration 2
step 1 readGen2FRAM A1 21 00 02 -> REFUSED: NFC error 0x10: … block read without an authenticated FRAM read (0x1F)  => LT: completion(nil) -> fail(.noSensorData)
step 2 readKeyIndex A1 22 -> 00000100; LT parseKeyIndex(offset 1) = 256 (credential NOT FOUND); Juggluco raw[3..4] = 1
step 3 enableStreamingRequest A1 1e2a000000eb3f6557 -> REFUSED: NFC error 0x02: … needs the authenticated 0x1E (A1 20 challenge first)
PROBE: LT's Gen2 NFC pairing FAILS at 3 of 3 steps
```

In LibreTransmitter's order step 1 already ends the pairing; steps 2 and 3 are shown to list every difference.
