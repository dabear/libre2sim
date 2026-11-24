#!/bin/sh
# Cross-check libre2sim's Gen2 sensor against Juggluco's Gen2 client code.
# Pass/fail: the handshake completes, every frame's CRC checks, and each
# decrypted plaintext equals what the sensor encrypted.
#
# Juggluco's protocol.{c,h} (GPL-3.0) are fetched at a pinned revision into a
# temp dir, not vendored. Override with JUGGLUCO_SRC=<dir with protocol.c/.h>.
set -eu
here=$(cd "$(dirname "$0")" && pwd)
root=$(cd "$here/../.." && pwd)
work=$(mktemp -d)
trap 'kill $spid 2>/dev/null || true; rm -rf "$work"' EXIT
spid=

REV=${JUGGLUCO_REV:-primary}
if [ -z "${JUGGLUCO_SRC:-}" ]; then
	JUGGLUCO_SRC=$work/juggluco
	mkdir -p "$JUGGLUCO_SRC"
	for f in protocol.c protocol.h; do
		curl -fsSL "https://raw.githubusercontent.com/j-kaltes/Juggluco/$REV/Common/src/main/cpp/libre2_imp/gen2/$f" -o "$JUGGLUCO_SRC/$f"
	done
fi
cc -O2 -std=c11 -I"$JUGGLUCO_SRC" -o "$work/client" "$here/juggluco_client.c" "$JUGGLUCO_SRC/protocol.c"
(cd "$root" && go build -o "$work/gen2sensor" ./cmd/gen2sensor)

frames=${FRAMES:-5}
fail=0
for slot in 1 2; do
	"$work/gen2sensor" -slot "$slot" -frames "$frames" >"$work/sensor.$slot" 2>&1 &
	spid=$!
	while ! grep -q '"addr"' "$work/sensor.$slot" 2>/dev/null; do sleep 0.1; done
	hdr=$(head -1 "$work/sensor.$slot")
	field() { printf '%s' "$hdr" | sed -E "s/.*\"$1\":\"?([^\",}]*).*/\1/"; }
	addr=$(field addr); uid=$(field uid); prov=$(field provisioning)
	"$work/client" "${addr%:*}" "${addr##*:}" "$uid" "$prov" "$slot" >"$work/client.$slot" 2>&1 || true
	wait $spid || true
	spid=
	want=$(sed -n 's/.*"plain":"\([0-9a-f]*\)".*/\1/p' "$work/sensor.$slot")
	got=$(sed -n 's/.*CRC OK plain=\([0-9a-f]*\).*/\1/p' "$work/client.$slot")
	if grep -q 'HANDSHAKE OK' "$work/client.$slot" &&
		[ "$(printf '%s\n' "$want" | grep -c .)" -eq "$frames" ] &&
		[ "$want" = "$got" ]; then
		echo "slot $slot: PASS (handshake, $frames/$frames CRC, plaintext match)"
	else
		echo "slot $slot: FAIL"; echo "--- sensor"; cat "$work/sensor.$slot"; echo "--- client"; cat "$work/client.$slot"
		fail=1
	fi
done
exit $fail
