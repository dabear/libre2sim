#!/bin/sh
# SPDX-License-Identifier: GPL-3.0-or-later
# Differential test of two Gen2 clients on random inputs: Juggluco's
# protocol.{c,h} (GPL-3.0) through juggluco_peer.c, and LibreTransmitter's
# Libre2Gen2.swift + Libre2Gen2Credentials.swift through lt_peer.swift. The
# driver (driver/main.go) uses libre2sim's Gen2 sensor for challenges, replies and
# frames. See driver/main.go for what each case checks.
#
# Sources are fetched at run time, not vendored. Overrides:
#   JUGGLUCO_SRC=<dir>  protocol.c/.h      (default: Juggluco @ $JUGGLUCO_REV, primary)
#   LT_SRC=<dir>        Libre2Gen2.swift and Libre2Gen2Credentials.swift
#                       (default: $LT_REPO @ $LT_REV)
#   CASES=1000  SEED=1
set -eu
here=$(cd "$(dirname "$0")" && pwd)
root=$(cd "$here/../.." && pwd)
work=$(mktemp -d); mkdir -p "$work/bin"
trap 'rm -rf "$work"' EXIT

JUGGLUCO_REV=${JUGGLUCO_REV:-primary}
LT_REPO=${LT_REPO:-bjorninge-no-personal/LibreTransmitterLibre2Gen2Experimental}
LT_REV=${LT_REV:-next-dev+libre2gen2}

if [ -z "${JUGGLUCO_SRC:-}" ]; then
	src=$work/juggluco; mkdir -p "$src"
	for f in protocol.c protocol.h; do
		curl -fsSL "https://raw.githubusercontent.com/j-kaltes/Juggluco/$JUGGLUCO_REV/Common/src/main/cpp/libre2_imp/gen2/$f" -o "$src/$f"
	done
else src=$JUGGLUCO_SRC; fi
cc -O2 -std=c11 -I"$src" -o "$work/bin/juggluco" "$here/juggluco_peer.c" "$src/protocol.c"

if [ -z "${LT_SRC:-}" ]; then
	src=$work/lt; mkdir -p "$src"
	rev=$(printf '%s' "$LT_REV" | sed 's/+/%2B/g')
	for f in Libre2Gen2.swift Libre2Gen2Credentials.swift; do
		curl -fsSL "https://raw.githubusercontent.com/$LT_REPO/$rev/LibreSensor/SensorContents/$f" -o "$src/$f"
	done
else src=$LT_SRC; fi
mkdir -p "$work/ltbuild"; cp "$here/lt_peer.swift" "$work/ltbuild/main.swift"
swiftc -O -o "$work/bin/lt" "$work/ltbuild/main.swift" "$src/Libre2Gen2.swift" "$src/Libre2Gen2Credentials.swift"

cd "$root"
GO111MODULE=on go run ./tools/gen2diff/driver -cases "${CASES:-1000}" -seed "${SEED:-1}" "$work/bin/juggluco" "$work/bin/lt"
