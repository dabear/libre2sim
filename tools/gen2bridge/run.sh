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
# Pass: the handshake completes, every frame's CRC checks, each decrypted
# plaintext equals what the sensor encrypted, and frames go out as 20 + 18 + 8
# notifies. juggluco-nfc also: NFC learns the sensor's slot and provisioning,
# the decrypted FRAM equals the sensor's with valid CRCs, and the BLE address
# matches.
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

CLIENTS=${CLIENTS:-juggluco juggluco-nfc lt}
FRAMES=${FRAMES:-5}
JUGGLUCO_REV=${JUGGLUCO_REV:-primary}
LT_REPO=${LT_REPO:-bjorninge-no-personal/LibreTransmitterLibre2Gen2Experimental}
LT_REV=${LT_REV:-next-dev+libre2gen2}

mkdir -p "$work/bin"
(cd "$root" && go build -o "$work/gen2sensor" ./cmd/gen2sensor)

for c in $CLIENTS; do
	case $c in
	juggluco-nfc) ;; # the juggluco binary
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
		nfcflag=; [ "$c" = juggluco-nfc ] && nfcflag=-nfc
		"$work/gen2sensor" $nfcflag -slot "$slot" -frames "$FRAMES" >"$log.sensor" 2>&1 &
		spid=$!
		while ! grep -q '"addr"' "$log.sensor" 2>/dev/null; do sleep 0.1; done
		hdr=$(head -1 "$log.sensor")
		field() { printf '%s' "$hdr" | sed -E "s/.*\"$1\":\"?([^\",}]*).*/\1/"; }
		addr=$(field addr); uid=$(field uid); prov=$(field provisioning)
		case $c in
		juggluco-nfc) "$work/bin/juggluco" "${addr%:*}" "${addr##*:}" "$uid" nfc - "$FRAMES" >"$log.client" 2>&1 || true ;;
		*) "$work/bin/$c" "${addr%:*}" "${addr##*:}" "$uid" "$prov" "$slot" "$FRAMES" >"$log.client" 2>&1 || true ;;
		esac
		wait $spid || true
		spid=
		want=$(sed -n 's/.*"plain":"\([0-9a-f]*\)".*/\1/p' "$log.sensor")
		got=$(sed -n 's/.*CRC OK plain=\([0-9a-f]*\).*/\1/p' "$log.client")
		chunks=$(grep -c '"notifies":\[20,18,8\]' "$log.sensor" || true)
		nfcok=yes nfcmsg=
		if [ "$c" = juggluco-nfc ]; then
			mac=$(field bleAddress | tr -d : | tr 'A-F' 'a-f' | sed 's/../& /g' | awk '{for(i=NF;i>0;i--)printf $i}')
			framwant=$(sed -n 's/.*"fram":"\([0-9a-f]*\)".*/\1/p' "$log.sensor")
			if grep -q '^NFC OK' "$log.client" && grep -q '^NFC framCRC=ok' "$log.client" && grep -q '^NFC activate=ok' "$log.client" &&
				grep -q "^NFC slot=$slot\$" "$log.client" && grep -q "^NFC provisioning=$prov\$" "$log.client" &&
				grep -q "^NFC address=$mac\$" "$log.client" && grep -q "^NFC fram=$framwant\$" "$log.client" &&
				grep -q '"streamingEnabledByNFC":true' "$log.sensor"; then
				nfcmsg="NFC: slot, provisioning, address, FRAM (CRCs ok) and activate match; "
			else nfcok=no; fi
		fi
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
