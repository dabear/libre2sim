#!/bin/sh
# SPDX-License-Identifier: GPL-3.0-or-later
# Cross-check libre2sim's Gen2 sensor (cmd/gen2sensor) against two Gen2
# clients, for root slots 1 and 2:
#   juggluco  Juggluco's protocol.{c,h} (GPL-3.0), via juggluco_client.c
#   lt        LibreTransmitter's Libre2Gen2.swift + Libre2Gen2Credentials.swift,
#             via lt_client.swift (macOS, swiftc)
# Pass: the handshake completes, every frame's CRC checks, and each decrypted
# plaintext equals what the sensor encrypted.
#
# Sources are fetched at run time, not vendored. Overrides:
#   JUGGLUCO_SRC=<dir>  protocol.c/.h      (default: Juggluco @ $JUGGLUCO_REV, primary)
#   LT_SRC=<dir>        the two Swift files (default: $LT_REPO @ $LT_REV)
#   CLIENTS="juggluco lt"   FRAMES=5
set -eu
here=$(cd "$(dirname "$0")" && pwd)
root=$(cd "$here/../.." && pwd)
work=$(mktemp -d)
spid=
trap 'kill $spid 2>/dev/null || true; rm -rf "$work"' EXIT

CLIENTS=${CLIENTS:-juggluco lt}
FRAMES=${FRAMES:-5}
JUGGLUCO_REV=${JUGGLUCO_REV:-primary}
LT_REPO=${LT_REPO:-bjorninge-no-personal/LibreTransmitterLibre2Gen2Experimental}
LT_REV=${LT_REV:-next-dev+libre2gen2}

mkdir -p "$work/bin"
(cd "$root" && go build -o "$work/gen2sensor" ./cmd/gen2sensor)

for c in $CLIENTS; do
	case $c in
	juggluco)
		if [ -z "${JUGGLUCO_SRC:-}" ]; then
			src=$work/juggluco; mkdir -p "$src"
			for f in protocol.c protocol.h; do
				curl -fsSL "https://raw.githubusercontent.com/j-kaltes/Juggluco/$JUGGLUCO_REV/Common/src/main/cpp/libre2_imp/gen2/$f" -o "$src/$f"
			done
		else src=$JUGGLUCO_SRC; fi
		cc -O2 -std=c11 -I"$src" -o "$work/bin/juggluco" "$here/juggluco_client.c" "$src/protocol.c" ;;
	lt)
		if [ -z "${LT_SRC:-}" ]; then
			src=$work/lt; mkdir -p "$src"
			rev=$(printf '%s' "$LT_REV" | sed 's/+/%2B/g')
			for f in Libre2Gen2.swift Libre2Gen2Credentials.swift; do
				curl -fsSL "https://raw.githubusercontent.com/$LT_REPO/$rev/LibreSensor/SensorContents/$f" -o "$src/$f"
			done
		else src=$LT_SRC; fi
		mkdir -p "$work/ltbuild"; cp "$here/lt_client.swift" "$work/ltbuild/main.swift"
		swiftc -O -o "$work/bin/lt" "$work/ltbuild/main.swift" "$src/Libre2Gen2.swift" "$src/Libre2Gen2Credentials.swift" ;;
	*) echo "unknown client $c"; exit 2 ;;
	esac
done

fail=0
for c in $CLIENTS; do
	for slot in 1 2; do
		log=$work/$c.$slot
		"$work/gen2sensor" -slot "$slot" -frames "$FRAMES" >"$log.sensor" 2>&1 &
		spid=$!
		while ! grep -q '"addr"' "$log.sensor" 2>/dev/null; do sleep 0.1; done
		hdr=$(head -1 "$log.sensor")
		field() { printf '%s' "$hdr" | sed -E "s/.*\"$1\":\"?([^\",}]*).*/\1/"; }
		addr=$(field addr); uid=$(field uid); prov=$(field provisioning)
		"$work/bin/$c" "${addr%:*}" "${addr##*:}" "$uid" "$prov" "$slot" "$FRAMES" >"$log.client" 2>&1 || true
		wait $spid || true
		spid=
		want=$(sed -n 's/.*"plain":"\([0-9a-f]*\)".*/\1/p' "$log.sensor")
		got=$(sed -n 's/.*CRC OK plain=\([0-9a-f]*\).*/\1/p' "$log.client")
		if grep -q 'HANDSHAKE OK' "$log.client" &&
			[ "$(printf '%s\n' "$want" | grep -c .)" -eq "$FRAMES" ] &&
			[ "$want" = "$got" ]; then
			echo "$c slot $slot: PASS (handshake, $FRAMES/$FRAMES CRC, plaintext match)"
		else
			echo "$c slot $slot: FAIL"; echo "--- sensor"; cat "$log.sensor"; echo "--- client"; cat "$log.client"
			fail=1
		fi
	done
done
exit $fail
