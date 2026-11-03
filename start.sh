#!/bin/sh
# Builds libre2sim and starts it, then opens the control panel.
#
#   ./start.sh                     a sensor worn for 3 hours, ready to pair (later runs resume it)
#   ./start.sh -fresh -age 10m     a new sensor, 10 minutes into its 1-hour warmup
#   ./start.sh -fresh -age 0       a new sensor still in its applicator
#   ./start.sh --no-impossible     skip building the ImpossiBLE library
#
# The first run also builds the ImpossiBLE library that the app in the Simulator needs
# (simulator/build/libImpossiBLE.dylib; it needs Xcode and network access), and rebuilds it
# whenever its patches change. --rebuild-impossible forces a rebuild.
#
# Other arguments go to libre2sim as flags (see ./libre2sim -h). The control API listens on
# 127.0.0.1:8081 unless you pass -api. Set LIBRE2SIM_NO_OPEN=1 to not open a browser.
set -e
cd "$(dirname "$0")"

impossible=auto
case "$1" in
--no-impossible) impossible=no; shift ;;
--rebuild-impossible) impossible=yes; shift ;;
esac

# The library is rebuilt when the build script or a patch changes, so a fix
# like patch 0008 is never missing from an old build.
dylib="$PWD/simulator/build/libImpossiBLE.dylib"
stamp="$PWD/simulator/build/.stamp"
want=$(cat simulator/build-impossible.sh simulator/impossible-patches/*.patch | shasum | cut -c1-16)
if [ "$impossible" = auto ] && { [ ! -f "$dylib" ] || [ "$(cat "$stamp" 2>/dev/null)" != "$want" ]; }; then
	impossible=yes
fi
if [ "$impossible" = yes ]; then
	if command -v xcrun >/dev/null 2>&1; then
		echo "Building the ImpossiBLE library (once; about a minute)..."
		sh simulator/build-impossible.sh simulator/build >/dev/null && echo "$want" > "$stamp"
	else
		echo "Skipping the ImpossiBLE library: it needs Xcode (macOS)." >&2
	fi
fi

if ! command -v go >/dev/null 2>&1; then
	echo "libre2sim needs Go 1.20 or later: https://go.dev/dl/ (or: brew install go)" >&2
	exit 1
fi
GO111MODULE=on go build -o libre2sim .

echo
echo "libre2sim: development use only, not an Abbott product; all data is simulated."
echo "Control panel: http://127.0.0.1:8081/"
if [ -f "$dylib" ]; then
	echo "For the app in the Simulator (Xcode: scheme > Run > Arguments > Environment Variables):"
	echo "  DYLD_INSERT_LIBRARIES=$dylib"
	echo "  IMPOSSIBLE_SOCKET=/tmp/impossible-libre2.sock"
	echo "  LIBRE2SIM_URL=http://127.0.0.1:8081"
else
	echo "No ImpossiBLE library, so no app in the Simulator can connect (run ./start.sh without --no-impossible)."
fi
echo

if [ -z "$LIBRE2SIM_NO_OPEN" ] && command -v open >/dev/null 2>&1; then
	(sleep 1 && open "http://127.0.0.1:8081/") &
fi

# Defaults first, so that flags you pass win (the last one counts).
exec ./libre2sim -api 127.0.0.1:8081 -age 3h "$@"
