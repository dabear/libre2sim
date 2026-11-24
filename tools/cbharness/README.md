# cbharness

The highest-fidelity check short of a full Loop build. It runs in the iOS Simulator with the real
`libImpossiBLE.dylib` injected, and uses **real CoreBluetooth** (`CBCentralManager`) driving
LibreTransmitter's **real `Libre2DirectTransmitter` plugin**:

1. It pairs with `SimulatedSensorPairingService` (LibreTransmitter #62) and stores
   `UserDefaults.standard.preSelectedSensor` as `Libre2DirectSetup` does.
2. It then plays `LibreTransmitterProxyManager`'s part, which can't be compiled on its own because it
   imports LoopKit: scan for `Libre2DirectTransmitter.serviceUUID`, match `name == MAC` plus
   `canSupportPeripheral`, connect, discover, and pass characteristics and values to the plugin.
   The plugin writes its own unlock payload, reassembles packets, and calls
   `libreSensorDidUpdate(with: bleData, …)`.

`Stubs.swift` stands in for the LoopKit-only types (`DeviceLogEntryType`, `BluetoothmanagerState`,
`LibreTransmitterDelegate`, `UserDefaults.optionalBool`).

`$L2SIM` is your libre2sim checkout. Build: in LibreTransmitter next-dev (from 924e933, #62, on; for an older one, apply `integration/LibreTransmitter`'s patch first):

```bash
H=$L2SIM/tools/cbharness
F=(LibreSensor/SensorPairing/*.swift LibreSensor/SensorContents/{PreLibre2,SensorData,CRC,Measurement,SensorState,SensorSerialNumber,Sensor}.swift \
   LibreSensor/GlucoseAlgorithm/GlucoseFromRaw.swift LibreSensor/{LimitedQueue,LibreError}.swift Bluetooth/{LibreTransmitterMetadata,CBPeripheralExtensions}.swift \
   Bluetooth/Transmitter/{Libre2DirectTransmitter,LibreTransmitterProxyProtocol,UUIDContainer,MiaomiaoTransmitter,BubbleTransmitter}.swift \
   Common/LocalizedString.swift Common/Extensions/*.swift Common/Settings/UIApplication+metadata.swift Features.swift)
# Sensor.swift lacks `import Foundation` (Xcode tolerates it), hence -import-module Foundation
xcrun -sdk iphonesimulator swiftc -swift-version 5 -target arm64-apple-ios17.0-simulator -module-name LibreTransmitter \
  -module-cache-path /tmp/cb-mc -Xfrontend -import-module -Xfrontend Foundation -o /tmp/cbharness "${F[@]}" $H/{Stubs,main}.swift
```

Run (libre2sim on `/tmp/impossible.sock`; dylib from `simulator/build-impossible.sh`):

```bash
SIMCTL_CHILD_DYLD_INSERT_LIBRARIES=/path/libImpossiBLE.dylib SIMCTL_CHILD_IMPOSSIBLE_SOCKET=/tmp/impossible.sock \
SIMCTL_CHILD_PACKETS=2 xcrun simctl spawn "iPhone 16" /tmp/cbharness
```

Modes (env):
- `PACKETS=n`: packets to wait for (default 2).
- `RECONNECT=1`: skip NFC and reuse the stored Sensor, as LibreTransmitter does for a known sensor. The plugin
  bumps `unlockCount` on every connection.
- `REPLAY=n` (with `RECONNECT=1`): rewind the stored count so the plugin sends count n again. Use it to
  check `-strict-unlock`.
- `FOLLOW=1`: on a drop, reconnect to the known peripheral at once, as `LibreTransmitterProxyManager` does
  (`requiresDelayedReconnect` is false for the Libre 2). Packets are counted across reconnects.
- `TOLERATE_ERRORS=1`: print a plugin error (such as a CRC failure) and keep going, instead of failing. Use it with
  `POST /fault {"kind":"badcrc"}` to check that the plugin decodes the packets after a bad one.
- `INJECT_GEN2=1` (**test only**): skip LibreTransmitter's NFC pairing and take the NFC results from libre2sim's
  `POST /gen2/test-pairing` (Gen2 mode on), then run the normal path: LibreTransmitter's Gen2 BLE login and frames. For
  checking the Gen2 BLE path while LibreTransmitter's own Gen2 NFC pairing cannot run against the sensor
  (see `tools/gen2nfcprobe`). Build with the Gen2 files too: add `Libre2Gen2,Libre2Gen2Credentials` to the
  `SensorContents` list and `Libre2Gen2DirectHandler` to the `Transmitter` list (on a LibreTransmitter with Gen2 support).
- `TIMEOUT=s`: give up after s seconds (default 150). The harness prints `RESULT: FAIL timeout`, which is the
  expected end for a run that checks that packets stop (terminate, expiry).
