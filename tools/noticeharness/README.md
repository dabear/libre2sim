# noticeharness

A minimal iOS Simulator app that shows the integration patch's `SimulatorPairingNotice` (the box LibreTransmitter's Libre 2
setup shows in the Simulator) above a Pair Sensor button wired to the real `SimulatedSensorPairingService`. It has no App
Transport Security exceptions, like Loop, so it also shows that plain HTTP to `127.0.0.1` works from an app.

Build, in LibreTransmitter with the patch applied (`$L2SIM` is your libre2sim checkout):

```bash
O=/tmp/nh; mkdir -p $O/NoticeHarness.app && cp $L2SIM/tools/noticeharness/Info.plist $O/NoticeHarness.app/
sed '/^import LibreTransmitter$/d' LibreTransmitterUI/Views/Setup/SimulatorPairingNotice.swift > $O/Notice.swift
F=(LibreSensor/SensorPairing/*.swift LibreSensor/SensorContents/{PreLibre2,SensorData,CRC,Measurement,SensorState,SensorSerialNumber}.swift \
   LibreSensor/GlucoseAlgorithm/GlucoseFromRaw.swift Bluetooth/LibreTransmitterMetadata.swift Common/LocalizedString.swift \
   Common/Extensions/{DataExtensions,DateExtensions,DoubleExtensions,TimeIntervalExtensions}.swift Features.swift)
xcrun -sdk iphonesimulator swiftc -swift-version 5 -parse-as-library -target arm64-apple-ios17.0-simulator -module-name LibreTransmitter \
  -module-cache-path $O/mc -o $O/NoticeHarness.app/NoticeHarness "${F[@]}" $O/Notice.swift $L2SIM/tools/noticeharness/Host.swift
codesign -s - -f $O/NoticeHarness.app
```

Run (`AUTOPAIR=1` taps Pair Sensor at launch; the `DYLD_INSERT_LIBRARIES` line turns step 2 green):

```bash
xcrun simctl install booted $O/NoticeHarness.app
SIMCTL_CHILD_AUTOPAIR=1 SIMCTL_CHILD_DYLD_INSERT_LIBRARIES=$L2SIM/simulator/build/libImpossiBLE.dylib \
  xcrun simctl launch --terminate-running-process booted org.libre2sim.noticeharness
xcrun simctl io booted screenshot notice.png
```

`integration/LibreTransmitter/images/` has the two states it was checked in: libre2sim running without the library, and
libre2sim stopped with the library loaded and a failed pairing.
