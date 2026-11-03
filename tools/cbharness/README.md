# cbharness

The highest-fidelity check short of a full Loop build. It runs in the iOS Simulator with the real
`libImpossiBLE.dylib` injected, and uses **real CoreBluetooth** (`CBCentralManager`) driving
LibreTransmitter's **real `Libre2DirectTransmitter` plugin**:

1. It pairs with `SimulatedSensorPairingService` (from the integration patch) and stores
   `UserDefaults.standard.preSelectedSensor` as `Libre2DirectSetup` does.
2. It then plays `LibreTransmitterProxyManager`'s part, which can't be compiled on its own because it
   imports LoopKit: scan for `Libre2DirectTransmitter.serviceUUID`, match `name == MAC` plus
   `canSupportPeripheral`, connect, discover, and pass characteristics and values to the plugin.
   The plugin writes its own unlock payload, reassembles packets, and calls
   `libreSensorDidUpdate(with: bleData, …)`.

`Stubs.swift` stands in for the LoopKit-only types (`DeviceLogEntryType`, `BluetoothmanagerState`,
`LibreTransmitterDelegate`, `UserDefaults.optionalBool`).

Build: in LibreTransmitter (next-dev @ f3e232e, or main @ 4110512) with the integration patch applied:

```bash
H=/Users/bjorninge/Code/libre2sim/tools/cbharness
F=(LibreSensor/SensorPairing/*.swift LibreSensor/SensorContents/{PreLibre2,SensorData,CRC,Measurement,SensorState,SensorSerialNumber,Sensor}.swift \
   LibreSensor/GlucoseAlgorithm/GlucoseFromRaw.swift LibreSensor/{LimitedQueue,LibreError}.swift Bluetooth/{LibreTransmitterMetadata,CBPeripheralExtensions}.swift \
   Bluetooth/Transmitter/{Libre2DirectTransmitter,LibreTransmitterProxyProtocol,UUIDContainer,MiaomiaoTransmitter,BubbleTransmitter}.swift \
   Common/LocalizedString.swift Common/Extensions/*.swift Common/Settings/UIApplication+metadata.swift Features.swift)
# Sensor.swift lacks `import Foundation` (Xcode tolerates it), hence -import-module Foundation
xcrun -sdk iphonesimulator swiftc -swift-version 5 -target arm64-apple-ios17.0-simulator -module-name LibreTransmitter \
  -module-cache-path /tmp/cb-mc -Xfrontend -import-module -Xfrontend Foundation -o /tmp/cbharness "${F[@]}" $H/{Stubs,main}.swift
```

Run (libre2sim on `/tmp/impossible-libre2.sock`; dylib from `simulator/build-impossible.sh`):

```bash
SIMCTL_CHILD_DYLD_INSERT_LIBRARIES=/path/libImpossiBLE.dylib SIMCTL_CHILD_IMPOSSIBLE_SOCKET=/tmp/impossible-libre2.sock \
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
- `TIMEOUT=s`: give up after s seconds (default 150). The harness prints `RESULT: FAIL timeout`, which is the
  expected end for a run that checks that packets stop (terminate, expiry).
