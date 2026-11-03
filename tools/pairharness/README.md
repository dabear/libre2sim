# pairharness

An independent end-to-end check of the NFC side. It compiles LibreTransmitter's **real** Swift sources
(with the `integration/LibreTransmitter` patch applied) plus `main.swift` into an iOS-Simulator
command-line binary. The binary runs `SimulatedSensorPairingService` against a live libre2sim and
prints what LibreTransmitter would store: UID, patch info, MAC, FRAM CRC validity, state, wear,
serial and calibration.

```bash
# 1. LibreTransmitter @ 4110512 with the patch applied
git clone https://github.com/LoopKit/LibreTransmitter /tmp/lt && cd /tmp/lt && git checkout 4110512
git am /Users/bjorninge/Code/libre2sim/integration/LibreTransmitter/0001-*.patch

# 2. build (the file list avoids LoopKit/UIKit; -swift-version 5 avoids a 6.x type-checker timeout)
H=/Users/bjorninge/Code/libre2sim/tools/pairharness
F=(LibreSensor/SensorPairing/*.swift LibreSensor/SensorContents/{PreLibre2,SensorData,CRC,Measurement,SensorState,SensorSerialNumber}.swift \
   LibreSensor/GlucoseAlgorithm/GlucoseFromRaw.swift Bluetooth/LibreTransmitterMetadata.swift Common/LocalizedString.swift \
   Common/Extensions/{DataExtensions,DateExtensions,DoubleExtensions,TimeIntervalExtensions}.swift Features.swift)
xcrun -sdk iphonesimulator swiftc -swift-version 5 -target arm64-apple-ios17.0-simulator \
  -module-name LibreTransmitter -module-cache-path /tmp/ph-mc -o /tmp/pairharness "${F[@]}" $H/main.swift

# 3. run (libre2sim must be listening on LIBRE2SIM_URL, default 127.0.0.1:8081)
xcrun simctl boot "iPhone 16"   # once
xcrun simctl spawn "iPhone 16" /tmp/pairharness
# to point it elsewhere: SIMCTL_CHILD_LIBRE2SIM_URL=http://127.0.0.1:8091 xcrun simctl spawn …
```

It prints `RESULT: paired` or `RESULT: cancelled (pairing failed)`, followed by details.
