#!/bin/sh
# Builds libre2sim and starts it, sets up the iOS Simulator for it, and opens the control panel.
#
#   ./start.sh                     a sensor worn for 3 hours, ready to pair (later runs resume it)
#   ./start.sh -fresh -age 10m     a new sensor, 10 minutes into its 1-hour warmup
#   ./start.sh -fresh -age 0       a new sensor still in its applicator
#   ./start.sh --no-impossible     skip building the ImpossiBLE library
#   ./start.sh --no-inject         don't set up the running Simulator(s); set DYLD_INSERT_LIBRARIES yourself
#
# The first run also builds the ImpossiBLE library that the app in the Simulator needs
# (simulator/build/libImpossiBLE.dylib; it needs Xcode and network access), and rebuilds it
# whenever its patches change. --rebuild-impossible forces a rebuild.
#
# Then it sets DYLD_INSERT_LIBRARIES inside every booted Simulator (launchctl setenv), so an app
# you launch there, from Xcode or the home screen, gets libre2sim's Bluetooth without any scheme
# change. The library does nothing in the Simulator's own processes (impossible-patches/0009).
# Ctrl-C stops libre2sim and removes the setting again.
#
# Other arguments go to libre2sim as flags (see ./libre2sim -h). The control API listens on
# 127.0.0.1:8081 unless you pass -api. Set LIBRE2SIM_NO_OPEN=1 to not open a browser.
set -e
cd "$(dirname "$0")"

impossible=auto inject=yes
while :; do
	case "$1" in
	--no-impossible) impossible=no; shift ;;
	--rebuild-impossible) impossible=yes; shift ;;
	--no-inject) inject=no; shift ;;
	*) break ;;
	esac
done

# The library is rebuilt when the build script or a patch changes, so a fix
# like patch 0008 is never missing from an old build.
# pwd -P gives the path's real letter case: the Simulator's dyld matches
# DYLD_INSERT_LIBRARIES case-sensitively, though macOS paths are not.
here=$(/bin/pwd -P)
dylib="$here/simulator/build/libImpossiBLE.dylib"
stamp="$here/simulator/build/.stamp"
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

# The socket the app should use: the default unless -socket is passed (the last one counts).
socket=/tmp/impossible.sock
prev=
for a in "$@"; do
	[ "$prev" = -socket ] && socket=$a
	case "$a" in -socket=*) socket=${a#-socket=} ;; esac
	prev=$a
done

# The booted Simulators, as "udid name" lines.
booted=
if [ "$inject" = yes ] && [ -f "$dylib" ] && command -v xcrun >/dev/null 2>&1; then
	booted=$(xcrun simctl list devices booted 2>/dev/null |
		sed -n 's/^ *\(.*\) (\([0-9A-F-]\{36\}\)) (Booted).*/\2 \1/p')
fi
setenv() { # udid
	xcrun simctl spawn "$1" launchctl setenv DYLD_INSERT_LIBRARIES "$dylib"
	if [ "$socket" != /tmp/impossible.sock ]; then
		xcrun simctl spawn "$1" launchctl setenv IMPOSSIBLE_SOCKET "$socket"
	fi
}
unsetenv() { # udid
	xcrun simctl spawn "$1" launchctl unsetenv DYLD_INSERT_LIBRARIES 2>/dev/null || true
	xcrun simctl spawn "$1" launchctl unsetenv IMPOSSIBLE_SOCKET 2>/dev/null || true
}
echo "$booted" | while read -r udid name; do [ -n "$udid" ] && setenv "$udid"; done

# Defaults first, so that flags you pass win (the last one counts).
./libre2sim -api 127.0.0.1:8081 -age 3h "$@" &
sim=$!
cleanup() {
	trap - INT TERM EXIT
	kill "$sim" 2>/dev/null || true
	wait "$sim" 2>/dev/null || true
	echo "$booted" | while read -r udid name; do [ -n "$udid" ] && unsetenv "$udid"; done
	if [ -n "$booted" ]; then
		echo "libre2sim stopped; apps launched in the Simulator from now on use its normal Bluetooth."
	fi
}
trap cleanup INT TERM EXIT

# Wait for the control API, then say what to do next.
i=0
while ! curl -sf http://127.0.0.1:8081/status >/dev/null 2>&1; do
	i=$((i + 1))
	if [ $i -gt 50 ] || ! kill -0 "$sim" 2>/dev/null; then
		echo "libre2sim did not start; see its output above." >&2
		exit 1
	fi
	sleep 0.2
done
state=$(curl -sf http://127.0.0.1:8081/status | sed -n 's/.*"framState": *\([0-9]\).*/\1/p')
case "$state" in
1) sensor="still in its applicator: activate it on the panel's Sensor page" ;;
2) sensor="warming up, so no readings yet (the panel's Sensor page can end the warmup)" ;;
3) sensor="ready to pair" ;;
*) sensor="ended: start a new one on the panel's Sensor page" ;;
esac
steps="   1. In Xcode, run Loop (the LoopWorkspace scheme) on that Simulator. Its LibreTransmitter
      must be next-dev from 924e933 (#62) on.
   2. In Loop: Settings → Add CGM → FreeStyle Libre → the Libre 2 direct option → Pair Sensor.
   3. Watch the panel's Event log: the NFC scan, the Bluetooth connect, then one packet a
      minute. Loop takes a reading about every 5 minutes."

echo
echo "====================================================================================="
echo " libre2sim is running: a simulated FreeStyle Libre 2 Plus (EU), $sensor."
echo " For development only, not an Abbott product. All data is simulated: never use it for"
echo " therapy or to make a treatment decision."
echo
echo " Control panel:  http://127.0.0.1:8081/"
echo
if [ ! -f "$dylib" ]; then
	echo " ! No ImpossiBLE library, so no app in the Simulator can connect."
	echo "   Run ./start.sh without --no-impossible (it needs Xcode)."
elif [ -n "$booted" ]; then
	echo " Bluetooth is set up in the running Simulator:"
	echo "$booted" | while read -r udid name; do echo "   - $name"; done
	echo "   Apps launched there from now on reach libre2sim; quit one that was already running"
	echo "   first. No scheme changes are needed. Booted another Simulator? Run ./start.sh again."
	echo
	echo " Next:"
	echo "$steps"
elif [ "$inject" = no ]; then
	echo " Add this to Loop's scheme (Edit Scheme → Run → Arguments → Environment Variables):"
	echo "   DYLD_INSERT_LIBRARIES = $dylib"
	[ "$socket" != /tmp/impossible.sock ] && echo "   IMPOSSIBLE_SOCKET     = $socket"
	echo
	echo " Then:"
	echo "$steps"
else
	echo " ! No Simulator is running, so nothing is set up there yet. Either:"
	echo "   - boot one (in Xcode: Open Developer Tool → Simulator, or: xcrun simctl boot \"iPhone 16\"),"
	echo "     then stop this with Ctrl-C and run ./start.sh again; or"
	echo "   - add this to Loop's scheme (Edit Scheme → Run → Arguments → Environment Variables):"
	echo "       DYLD_INSERT_LIBRARIES = $dylib"
	echo
	echo " Then:"
	echo "$steps"
fi
echo
if [ -n "$booted" ]; then
	echo " Ctrl-C stops libre2sim and removes the Bluetooth setup from the Simulator."
else
	echo " Ctrl-C stops libre2sim."
fi
echo "====================================================================================="
echo

if [ -z "$LIBRE2SIM_NO_OPEN" ] && command -v open >/dev/null 2>&1; then
	open "http://127.0.0.1:8081/"
fi
wait "$sim"
