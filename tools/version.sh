#!/bin/sh
# version.sh — print the version Murmel shows in its About dialog.
#
#   0.3.7                    on the release tag v0.3.7 (clean tree)
#   0.3.7-dev+5.g0782834     5 commits after it; ".dirty" with uncommitted
#                            changes to tracked files
#   0.3.7-dev                without git (a source tarball or a Flatpak
#                            build, which does not see .git)
#
# A file named build-version in the source root wins: tools/testbuild.sh
# and tools/release.sh write it for Flatpak builds, which run without .git.
set -eu
cd "$(dirname "$0")/.."
if [ -s build-version ]; then
	cat build-version
	exit 0
fi
base=$(cat VERSION)
if ! git rev-parse --git-dir >/dev/null 2>&1; then
	echo "$base-dev"
	exit 0
fi
dirty=
git diff --quiet HEAD -- 2>/dev/null || dirty=.dirty
if [ "$(git tag --points-at HEAD --list "v$base")" = "v$base" ] && [ -z "$dirty" ]; then
	echo "$base"
	exit 0
fi
# v0.3.7-5-g0782834 → 5 commits since the last release tag, at 0782834.
desc=$(git describe --tags --long --match 'v[0-9]*' 2>/dev/null || true)
if [ -n "$desc" ]; then
	n=${desc%-g*}
	n=${n##*-}
	echo "$base-dev+$n.g$(git rev-parse --short=7 HEAD)$dirty"
else
	echo "$base-dev+g$(git rev-parse --short=7 HEAD)$dirty"
fi
