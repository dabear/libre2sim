# swiftvectors

Prints golden test vectors for the Go port in `libre2/` (crypto, CRC, FRAM and BLE
decryption, enable-streaming parameters, streaming unlock payload) by running
LibreTransmitter's own Swift code. The output is committed as
`testdata/swift_vectors.json` and checked by `libre2/vectors_test.go`.

Files:

- `PreLibre2.swift`, `CRC.swift`, `FakeSensorPairingData.swift`: verbatim copies from
  [LoopKit/LibreTransmitter](https://github.com/LoopKit/LibreTransmitter) @
  `41105123c3c4fb5c55ce89a38630cea2f8f7a17a` (MIT), with an attribution header added.
- `Pairing.swift`: `nfcCommand`, the `UInt16` initialisers, `NFCCommand` and `Subcommand` from
  `SensorPairingService.swift`, copied verbatim but moved out of the CoreNFC class.
- `Stubs.swift`: minimal `SensorType`, `Measurement` and `SensorPairingInfo` so the copies compile.
- `main.swift`: computes the vectors and prints them as JSON (sorted keys, deterministic).

Regenerate (macOS with Xcode command-line tools), from the repository root:

```sh
mkdir -p /tmp/swiftvectors-build &&
swiftc -O -module-cache-path /tmp/swiftvectors-build/mc -o /tmp/swiftvectors-build/swiftvectors tools/swiftvectors/*.swift &&
/tmp/swiftvectors-build/swiftvectors > testdata/swift_vectors.json
```

Then `go test ./libre2/`.

The `decryptFRAM` entry for `FakeSensorPairingData` records `false` for all three CRCs: that xDrip
sample's FRAM does not match its patch info, in Swift as in Go (see `TestFRAMXDripSample`).
