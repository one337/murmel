#!/usr/bin/env bash
# testbuild.sh — build the current tree as a Flatpak and install it for
# the current user as the branch "master", next to a release install
# ("stable"), and make it the one that starts.
#
# The About dialog shows the development version (tools/version.sh, e.g.
# 0.3.7-dev+5.g0782834). Back to the release:
#   flatpak make-current --user io.github.one337.murmel stable
set -euo pipefail
cd "$(dirname "$0")/.."
APP_ID=io.github.one337.murmel
rm -f build-version
version=$(tools/version.sh)
echo "$version" >build-version
trap 'rm -f build-version' EXIT
flatpak-builder --user --install --force-clean --default-branch=master \
	build-flatpak packaging/$APP_ID.json
flatpak make-current --user $APP_ID master
echo "testbuild: installed $APP_ID $version (branch master)"
