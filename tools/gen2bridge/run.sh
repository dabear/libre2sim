#!/bin/sh
# SPDX-License-Identifier: GPL-3.0-or-later
# Cross-check libre2sim's Gen2 sensor (cmd/gen2sensor, the Sensor in Gen2
# mode) against Gen2 clients, for root slots 1 and 2:
#   juggluco      Juggluco's protocol.{c,h} (GPL-3.0), via juggluco_client.c:
#                 BLE only, given the NFC results
#   juggluco-nfc  the same, but first Juggluco's NFC flow (Gen2.java: root slot,
#                 authenticated FRAM read, activate, enable streaming), then BLE
#                 with what NFC gave
#   lt            LibreTransmitter's Libre2Gen2.swift + Libre2Gen2Credentials.swift,
#                 via lt_client.swift (macOS, swiftc): BLE only
#   lt-nfc        LibreTransmitter's own Gen2 NFC pairing (Libre2Gen2Pairing.pair
#                 from SensorPairingService+Gen2.swift, via lt_nfc_client.swift),
#                 then BLE with what it returned. SKIPs when $LT_REV has no
#                 Libre2Gen2Pairing (the old, unauthenticated pairing).
# Pass: the handshake completes, every frame's CRC checks, each decrypted
# plaintext equals what the sensor encrypted, and frames go out as 20 + 18 + 8
# notifies. juggluco-nfc also: NFC learns the sensor's slot and provisioning,
# the decrypted FRAM equals the sensor's with valid CRCs, and the BLE address
# matches. lt-nfc also: slot, provisioning, BLE address and FRAM match.
#
# Sources are fetched at run time, not vendored. Overrides:
#   JUGGLUCO_SRC=<dir>  protocol.c/.h      (default: Juggluco @ $JUGGLUCO_REV, primary)
#   LT_SRC=<dir>        the Swift files: Libre2Gen2.swift, Libre2Gen2Credentials.swift
#                       and, for lt-nfc, SensorPairingService+Gen2.swift
#                       (default: $LT_REPO @ $LT_REV)
#   CLIENTS="juggluco juggluco-nfc lt lt-nfc"   FRAMES=5   CLIENT_TIMEOUT=60 (seconds)
set -eu
here=$(cd "$(dirname "$0")" && pwd)
root=$(cd "$here/../.." && pwd)
work=$(mktemp -d)
spid=
trap 'kill $spid 2>/dev/null || true; rm -rf "$work"' EXIT

CLIENTS=${CLIENTS:-juggluco juggluco-nfc lt lt-nfc}
FRAMES=${FRAMES:-5}
JUGGLUCO_REV=${JUGGLUCO_REV:-primary}
LT_REPO=${LT_REPO:-bjorninge-no-personal/LibreTransmitterLibre2Gen2Experimental}
LT_REV=${LT_REV:-next-dev+libre2gen2}

mkdir -p "$work/bin"
# A client that hangs fails after CLIENT_TIMEOUT seconds instead of stalling the run.
to() { perl -e 'alarm shift; exec @ARGV' "$@"; }
CLIENT_TIMEOUT=${CLIENT_TIMEOUT:-60}
(cd "$root" && go build -o "$work/gen2sensor" ./cmd/gen2sensor)

for c in $CLIENTS; do
	case $c in
	juggluco|juggluco-nfc)
		[ -x "$work/bin/juggluco" ] && continue # both use the one binary
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
	lt-nfc)
		# LibreTransmitter's Libre2Gen2Pairing.pair: needs the Gen2 crypto and the
		# CoreNFC-free part of SensorPairingService+Gen2.swift.
		if [ -z "${LT_SRC:-}" ]; then
			src=$work/ltnfc; mkdir -p "$src"
			rev=$(printf '%s' "$LT_REV" | sed 's/+/%2B/g')
			for f in SensorContents/Libre2Gen2.swift SensorContents/Libre2Gen2Credentials.swift SensorPairing/SensorPairingService+Gen2.swift; do
				curl -fsSL "https://raw.githubusercontent.com/$LT_REPO/$rev/LibreSensor/$f" -o "$src/$(basename "$f")"
			done
		else src=$LT_SRC; fi
		b=$work/ltnfcbuild; mkdir -p "$b"
		if ! grep -q '^enum Libre2Gen2Pairing' "$src/SensorPairingService+Gen2.swift"; then
			skip_ltnfc="no Libre2Gen2Pairing in $LT_REPO@$LT_REV (its Gen2 NFC pairing is the old, unauthenticated one, before the fork's bf59b49)"
			continue
		fi
		{ echo 'import Foundation'
		  awk '/^struct Libre2Gen2PairingResult/{on=1} /^extension Libre2Gen2PairingResult/{on=0} on' "$src/SensorPairingService+Gen2.swift"
		  echo '// Stub: run.sh checks the FRAM byte for byte against the sim instead.'
		  echo 'struct SensorData { let hasValidCRCs = true; init?(bytes: [UInt8]) { guard bytes.count == 344 else { return nil } } }'
		} >"$b/pairing.swift"
		cp "$here/lt_nfc_client.swift" "$b/main.swift"
		swiftc -O -o "$work/bin/lt-nfc" "$b/main.swift" "$b/pairing.swift" "$src/Libre2Gen2.swift" "$src/Libre2Gen2Credentials.swift" ;;
	*) echo "unknown client $c"; exit 2 ;;
	esac
done

fail=0
for c in $CLIENTS; do
	if [ "$c" = lt-nfc ] && [ -n "${skip_ltnfc:-}" ]; then echo "lt-nfc: SKIP ($skip_ltnfc)"; continue; fi
	for slot in 1 2; do
		log=$work/$c.$slot
		nfcflag=; case $c in *-nfc) nfcflag=-nfc ;; esac
		"$work/gen2sensor" $nfcflag -slot "$slot" -frames "$FRAMES" >"$log.sensor" 2>&1 &
		spid=$!
		while ! grep -q '"addr"' "$log.sensor" 2>/dev/null; do sleep 0.1; done
		hdr=$(head -1 "$log.sensor")
		field() { printf '%s' "$hdr" | sed -E "s/.*\"$1\":\"?([^\",}]*).*/\1/"; }
		addr=$(field addr); uid=$(field uid); prov=$(field provisioning)
		case $c in
		juggluco-nfc) to "$CLIENT_TIMEOUT" "$work/bin/juggluco" "${addr%:*}" "${addr##*:}" "$uid" nfc - "$FRAMES" >"$log.client" 2>&1 || true ;;
		lt-nfc) echo "$uid" | to "$CLIENT_TIMEOUT" "$work/bin/lt-nfc" "${addr%:*}" "${addr##*:}" "$FRAMES" >"$log.client" 2>&1 || true ;;
		*) to "$CLIENT_TIMEOUT" "$work/bin/$c" "${addr%:*}" "${addr##*:}" "$uid" "$prov" "$slot" "$FRAMES" >"$log.client" 2>&1 || true ;;
		esac
		# The sensor has printed everything the client could have seen; don't
		# wait on it (a client that quit early leaves it blocked on the socket).
		sleep 0.3; kill $spid 2>/dev/null || true; wait $spid 2>/dev/null || true
		spid=
		want=$(sed -n 's/.*"plain":"\([0-9a-f]*\)".*/\1/p' "$log.sensor")
		got=$(sed -n 's/.*CRC OK plain=\([0-9a-f]*\).*/\1/p' "$log.client")
		chunks=$(grep -c '"notifies":\[20,18,8\]' "$log.sensor" || true)
		nfcok=yes nfcmsg=
		case $c in *-nfc)
			mac=$(field bleAddress | tr -d : | tr 'A-F' 'a-f' | sed 's/../& /g' | awk '{for(i=NF;i>0;i--)printf $i}')
			framwant=$(sed -n 's/.*"fram":"\([0-9a-f]*\)".*/\1/p' "$log.sensor")
			extra=yes
			if [ "$c" = juggluco-nfc ]; then
				grep -q '^NFC framCRC=ok' "$log.client" && grep -q '^NFC activate=ok' "$log.client" || extra=no
			fi
			if [ $extra = yes ] && grep -q '^NFC OK' "$log.client" &&
				grep -q "^NFC slot=$slot\$" "$log.client" && grep -q "^NFC provisioning=$prov\$" "$log.client" &&
				grep -q "^NFC address=$mac\$" "$log.client" && grep -q "^NFC fram=$framwant\$" "$log.client" &&
				grep -q '"streamingEnabledByNFC":true' "$log.sensor"; then
				if [ "$c" = juggluco-nfc ]; then nfcmsg="NFC: slot, provisioning, address, FRAM (CRCs ok) and activate match; "
				else nfcmsg="NFC: slot, provisioning, address and FRAM match; "; fi
			else nfcok=no; fi ;;
		esac
		if [ $nfcok = yes ] && grep -q 'HANDSHAKE OK' "$log.client" &&
			[ "$(printf '%s\n' "$want" | grep -c .)" -eq "$FRAMES" ] && [ "$chunks" -eq "$FRAMES" ] &&
			[ "$want" = "$got" ]; then
			echo "$c slot $slot: PASS (${nfcmsg}handshake, $FRAMES/$FRAMES CRC, plaintext match, 20+18+8)"
		else
			echo "$c slot $slot: FAIL"; echo "--- sensor"; cat "$log.sensor"; echo "--- client"; cat "$log.client"
			fail=1
		fi
	done
done
exit $fail
