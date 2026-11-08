#!/usr/bin/env bash
# xvfb-run-ui.sh — run a UI binary on a headless X server and screenshot it.
#
# Usage:
#   xvfb-run-ui.sh <binary> <screenshot.png> [lifetime_seconds]
#
#   binary            executable that opens a window
#   screenshot.png    output path; must end up larger than 10 KB
#   lifetime_seconds  how long the binary may run before the screenshot
#                     (default: 5)
#
# Environment:
#   USE_EXISTING_DISPLAY=1  do not start Xvfb; reuse $DISPLAY instead
#                           (for machines that have a real display)
#
# Exit codes:
#   0  screenshot captured and larger than 10 KB
#   1  binary/Xvfb failed, or screenshot missing/too small (e.g. crash)
#   2  bad usage
#   3  Xvfb or ImageMagick 'import' not installed

set -euo pipefail

usage() {
	sed -n '2,20p' "$0" >&2
	exit 2
}

[ "$#" -ge 2 ] && [ "$#" -le 3 ] || usage
binary=$1
png=$2
lifetime=${3:-5}

case "$lifetime" in
'' | *[!0-9]*) echo "xvfb-run-ui.sh: lifetime must be a positive integer" >&2 && usage ;;
esac

if ! command -v Xvfb >/dev/null 2>&1; then
	echo "ERROR: Xvfb is not installed — headless UI tests cannot run." >&2
	echo "       Install it with: sudo dnf install xorg-server-xvfb" >&2
	exit 3
fi
if ! command -v import >/dev/null 2>&1; then
	echo "ERROR: ImageMagick 'import' is not installed — screenshots cannot be taken." >&2
	echo "       Install it with: sudo dnf install ImageMagick" >&2
	exit 3
fi

xvfb_pid=""
child_pgid=""
cleanup() {
	if [ -n "$child_pgid" ]; then
		kill -- -"$child_pgid" 2>/dev/null || true
	fi
	if [ -n "$xvfb_pid" ]; then
		kill "$xvfb_pid" 2>/dev/null || true
	fi
}
trap cleanup EXIT

if [ "${USE_EXISTING_DISPLAY:-0}" = "1" ]; then
	if [ -z "${DISPLAY:-}" ]; then
		echo "xvfb-run-ui.sh: USE_EXISTING_DISPLAY=1 but DISPLAY is not set" >&2
		exit 2
	fi
else
	DISPLAY=:99 export DISPLAY
	Xvfb :99 -screen 0 1280x800x24 -nolisten tcp &
	xvfb_pid=$!
	for _ in $(seq 1 50); do
		[ -S /tmp/.X11-unix/X99 ] && break
		kill -0 "$xvfb_pid" 2>/dev/null || {
			echo "xvfb-run-ui.sh: Xvfb died on startup (stale /tmp/.X99-lock?)" >&2
			exit 1
		}
		sleep 0.1
	done
	[ -S /tmp/.X11-unix/X99 ] || {
		echo "xvfb-run-ui.sh: Xvfb socket did not appear within 5s" >&2
		exit 1
	}
fi

# setsid puts the binary into its own session; its pid doubles as the process
# group id, so the cleanup trap can kill the whole tree with kill -- -pid.
# sh -c + exec: lets $binary carry arguments (quoted or not); exec replaces
# the shell so the pid/pgid semantics stay intact.
#
# GDK_BACKEND=x11 + clearing WAYLAND_DISPLAY: on Wayland hosts the inherited
# WAYLAND_DISPLAY makes GTK apps attach to the host compositor instead of the
# Xvfb screen, producing blank (263-byte) screenshots. This was ISSUE-002.
GDK_BACKEND=x11 WAYLAND_DISPLAY= setsid sh -c "exec $binary" &
child_pgid=$!

sleep "$lifetime"
import -window root "$png"

kill -- -"$child_pgid" 2>/dev/null || true
child_pgid=""

size=$(stat -c%s "$png" 2>/dev/null || echo 0)
if [ "$size" -le 10240 ]; then
	echo "xvfb-run-ui.sh: screenshot $png is only $size bytes (expected > 10240)" >&2
	exit 1
fi
echo "xvfb-run-ui.sh: OK — $png ($size bytes)"
