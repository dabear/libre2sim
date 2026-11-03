#!/bin/bash
# Shows that the integration patch changes nothing in a device build of LibreTransmitter.
# usage: devicecheck.sh <LibreTransmitter checkout> <the same with the patch applied> <scratch dir>
#  1. each patched file, as a device build sees it (#if targetEnvironment(simulator) resolved), against the original
#  2. the pairing code compiled for arm64 iOS (-O, whole module) from the same path, compared section by section
set -e
orig=$1 patched=$2 work=$3
here=$(cd "$(dirname "$0")" && pwd)
mkdir -p "$work"
for f in $(git -C "$patched" diff --name-only "$(git -C "$orig" rev-parse HEAD)" -- '*.swift'); do
	echo "== $f"
	o="$orig/$f"
	[ -f "$o" ] || o=/dev/null # a new file: a device build should see nothing but its comments
	diff <(python3 -I "$here/device_view.py" "$o") <(python3 -I "$here/device_view.py" "$patched/$f") || true
done
for v in orig patched; do
	eval src=\$$v
	rm -rf "$work/src" && cp -R "$src" "$work/src"
	(cd "$work/src" && xcrun -sdk iphoneos swiftc -swift-version 5 -target arm64-apple-ios17.0 -O -wmo -parse-as-library \
		-suppress-warnings -module-name LibreTransmitter -module-cache-path "$work/mc" -c \
		LibreSensor/SensorPairing/*.swift LibreSensor/SensorContents/PreLibre2.swift LibreSensor/SensorContents/SensorData.swift \
		LibreSensor/SensorContents/CRC.swift LibreSensor/SensorContents/Measurement.swift LibreSensor/SensorContents/SensorState.swift \
		LibreSensor/SensorContents/SensorSerialNumber.swift LibreSensor/GlucoseAlgorithm/GlucoseFromRaw.swift \
		Bluetooth/LibreTransmitterMetadata.swift Common/LocalizedString.swift Common/Extensions/DataExtensions.swift \
		Common/Extensions/DateExtensions.swift Common/Extensions/DoubleExtensions.swift Common/Extensions/TimeIntervalExtensions.swift \
		Features.swift -o "$work/$v.o")
done
echo "== object files (differing bytes, by section)"
cmp -l "$work/orig.o" "$work/patched.o" | awk '{print $1-1}' | python3 -I "$here/sections.py" "$work/patched.o"
