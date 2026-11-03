#!/bin/bash
# Builds algoharness from a LibreTransmitter checkout, for the iOS Simulator.
# usage: build.sh <LibreTransmitter checkout> <output binary> [-D EXPERIMENTAL]
set -e
lt=$1 out=$2; shift 2
here=$(cd "$(dirname "$0")" && pwd)
cd "$lt"
# The files the glucose algorithm needs, plus what they reference (as cbharness).
files=(LibreSensor/SensorPairing/*.swift LibreSensor/SensorContents/*.swift
  LibreSensor/GlucoseAlgorithm/GlucoseFromRaw.swift LibreSensor/{LimitedQueue,LibreError}.swift
  Bluetooth/{LibreTransmitterMetadata,CBPeripheralExtensions}.swift
  Bluetooth/Transmitter/{Libre2DirectTransmitter,LibreTransmitterProxyProtocol,UUIDContainer,MiaomiaoTransmitter,BubbleTransmitter}.swift
  Common/LocalizedString.swift Common/Extensions/*.swift Common/Settings/UIApplication+metadata.swift Features.swift)
for f in Bluetooth/Transmitter/Libre2Gen2DirectHandler.swift; do [ -f "$f" ] && files+=("$f"); done
# Sensor.swift lacks `import Foundation` (Xcode tolerates it), hence -import-module Foundation.
xcrun -sdk iphonesimulator swiftc -swift-version 5 -target arm64-apple-ios17.0-simulator -module-name LibreTransmitter \
  -module-cache-path /tmp/algo-mc -Xfrontend -import-module -Xfrontend Foundation "$@" -o "$out" \
  "${files[@]}" "$here/../cbharness/Stubs.swift" "$here/main.swift"
