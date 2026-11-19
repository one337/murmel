#!/usr/bin/env bash
set -euo pipefail

cd "$(dirname "$0")/.."

failures=0

step() {
  printf '==> %s\n' "$1"
}

pass() {
  printf 'PASS: %s\n\n' "$1"
}

fail() {
  printf 'FAIL: %s\n\n' "$1"
  failures=$((failures + 1))
}

# The repository's own packages: not vendor/, not the untracked target/
# scratch programs that may exist in a working copy.
mapfile -t PKGS < <(go list ./... | grep -v '/target/')
mapfile -t BUILD_PKGS < <(go list -f '{{if .GoFiles}}{{.ImportPath}}{{end}}' ./... | grep -v '/target/')
# Go files in git and new ones not added yet; .gitignore (target/, ...)
# keeps scratch programs out.
mapfile -t GOFILES < <(git ls-files --cached --others --exclude-standard '*.go' ':!:vendor/**')
# Packages without GTK widgets: fast enough for the race detector (the UI
# packages compile gotk4 with -race, which takes many minutes).
mapfile -t RACE_PKGS < <(printf '%s\n' "${PKGS[@]}" | grep -v '/internal/ui')

step "gofmt (tracked and new files)"
if gofmt_output="$(gofmt -l "${GOFILES[@]}" 2>&1)" && [ -z "$gofmt_output" ]; then
  pass "gofmt (no unformatted files)"
else
  printf '%s\n' "$gofmt_output"
  fail "gofmt (files above need formatting)"
fi

step "go vet"
if go vet "${PKGS[@]}" 2>&1; then
  pass "go vet"
else
  fail "go vet"
fi

step "go build"
if go build "${BUILD_PKGS[@]}" 2>&1; then
  pass "go build"
else
  fail "go build"
fi

# Every module linked into the binary needs its license shipped (see
# tools/licenses.txt), and every listed license file must exist.
step "licenses: tools/licenses.txt covers the linked modules"
lic_problems=()
while read -r mod; do
  awk -v m="$mod" '!/^#/ && $2 == m { found = 1 } END { exit !found }' tools/licenses.txt ||
    lic_problems+=("module $mod has no line in tools/licenses.txt")
done < <(go list -deps -f '{{with .Module}}{{.Path}}{{end}}' ./cmd/murmel | sort -u | grep -vx 'io.github/murmel')
while read -r f; do
  [ -f "$f" ] || lic_problems+=("missing license file $f")
done < <(awk '!/^#/ && NF >= 3 { for (i = 3; i <= NF; i++) print $i }' tools/licenses.txt)
if [ ${#lic_problems[@]} -eq 0 ]; then
  pass "licenses"
else
  printf '  %s\n' "${lic_problems[@]}"
  fail "licenses"
fi

step "go test"
# The GTK tests (TestGTK…) need a display; they run in their own step below.
if go test -skip '^TestGTK' "${PKGS[@]}" 2>&1; then
  pass "go test"
else
  fail "go test"
fi

step "go test -race (packages without GTK widgets)"
if go test -race "${RACE_PKGS[@]}" 2>&1; then
  pass "race detector"
else
  fail "race detector"
fi

step "govulncheck"
GOVULNCHECK=(govulncheck)
if ! command -v govulncheck >/dev/null 2>&1; then
  GOVULNCHECK=(env GOFLAGS= go run golang.org/x/vuln/cmd/govulncheck@v1.9.0)
fi
if "${GOVULNCHECK[@]}" "${PKGS[@]}" 2>&1; then
  pass "govulncheck (no known vulnerabilities reached)"
else
  fail "govulncheck"
fi

step "glib-compile-schemas data/"
if glib-compile-schemas data/ 2>&1; then
  pass "glib-compile-schemas"
else
  fail "glib-compile-schemas"
fi

step "GSettings roundtrip (internal/settings, schema dir)"
GSETTINGS_TEST="env GSETTINGS_SCHEMA_DIR=$PWD/data go test ./internal/settings -run TestGSettingsRoundtrip"
if command -v xvfb-run >/dev/null 2>&1; then
  GSETTINGS_TEST="env GSETTINGS_SCHEMA_DIR=$PWD/data xvfb-run -a go test ./internal/settings -run TestGSettingsRoundtrip"
fi
if $GSETTINGS_TEST 2>&1; then
  pass "GSettings roundtrip"
else
  fail "GSettings roundtrip"
fi

# Leak tests need GTK and a display: they run on virtual X servers in a
# throwaway HOME, without the desktop's D-Bus session or Wayland display.
# Every package gets its own X server: without a window manager the input
# focus goes to the window shown last, so test programs sharing a display
# take it from each other. The packages still run in parallel. -v keeps
# the tests' log lines (repeated passes, windows shown again) in the output.
GTK_TEST_PKGS=(./internal/ui/channeltree ./internal/ui/admin ./internal/ui/connection ./internal/ui/prefs ./internal/ui/richtext)
step "GTK leak tests (xvfb-run)"
if command -v xvfb-run >/dev/null 2>&1; then
  GTKHOME=$(mktemp -d /tmp/murmel-gtk-XXXXXX)
  mkdir -m 700 "$GTKHOME/run"
  gtk_start=$SECONDS
  gtk_pids=()
  for i in "${!GTK_TEST_PKGS[@]}"; do
    # Each search for a free display starts elsewhere (-n before -a:
    # xvfb-run applies them in order), so parallel runs do not pick the
    # same one.
    env -u WAYLAND_DISPLAY -u DISPLAY \
      HOME="$GTKHOME" XDG_CONFIG_HOME="$GTKHOME/config" XDG_DATA_HOME="$GTKHOME/data" \
      XDG_CACHE_HOME="$GTKHOME/cache" XDG_STATE_HOME="$GTKHOME/state" XDG_RUNTIME_DIR="$GTKHOME/run" \
      DBUS_SESSION_BUS_ADDRESS=disabled: GDK_BACKEND=x11 GDK_DEBUG=no-portals GSETTINGS_BACKEND=memory \
      MESA_SHADER_CACHE_DISABLE=true \
      GOCACHE="$(go env GOCACHE)" GOMODCACHE="$(go env GOMODCACHE)" GOPATH="$(go env GOPATH)" \
      MURMEL_GTK_TESTS=1 xvfb-run -n $((100 + 10 * i)) -a \
      go test -count=1 -v -run '^TestGTK' "${GTK_TEST_PKGS[$i]}" >"$GTKHOME/test-$i.log" 2>&1 &
    gtk_pids+=($!)
  done
  gtk_failed=()
  for i in "${!GTK_TEST_PKGS[@]}"; do
    if ! wait "${gtk_pids[$i]}"; then
      gtk_failed+=("${GTK_TEST_PKGS[$i]}")
    fi
    cat "$GTKHOME/test-$i.log"
  done
  printf 'GTK tests took %ds\n' $((SECONDS - gtk_start))
  if [ ${#gtk_failed[@]} -eq 0 ]; then
    pass "GTK leak tests"
  else
    fail "GTK leak tests (${gtk_failed[*]})"
  fi
  rm -rf "$GTKHOME"
else
  printf 'xvfb-run is not installed (Fedora: xorg-x11-server-Xvfb)\n'
  fail "GTK leak tests need xvfb-run"
fi

step "i18n: TestNoHardcodedStrings"
if go test ./internal/ui -run TestNoHardcodedStrings 2>&1; then
  pass "no hardcoded UI strings"
else
  fail "hardcoded strings found (wrap them in tr())"
fi

step "i18n: POT file up-to-date"
TMPOT=$(mktemp /tmp/murmel-XXXXXX.pot)
tools/update-pot.sh "$TMPOT"
# Strip volatile header lines before diffing
strip_header() { grep -v "^\"POT-Creation-Date\|^\"PO-Revision-Date\|^# FIRST AUTHOR" "$1"; }
if diff <(strip_header po/murmel.pot) <(strip_header "$TMPOT") >/dev/null 2>&1; then
  pass "po/murmel.pot is current"
else
  printf 'Run: tools/update-pot.sh\n'
  fail "po/murmel.pot is out of date"
fi
rm -f "$TMPOT"

step "i18n: translations compile"
if tools/install-translations.sh 2>&1; then
  pass "po/*.po in po/LINGUAS compile"
else
  fail "a translation does not compile (msgfmt --check)"
fi
# Informational: open messages per language (they show in English).
for po in po/*.po; do
  [ -e "$po" ] || continue
  printf '  %s: %s\n' "$(basename "$po" .po)" "$(msgfmt --statistics -o /dev/null "$po" 2>&1)"
done

step "desktop file: desktop-file-validate"
TMPDESKTOP=$(mktemp -d /tmp/murmel-desktop-XXXXXX)
if msgfmt --desktop --template data/io.github.one337.murmel.desktop.in -d po \
  --keyword= --keyword=Comment --keyword=Keywords \
  -o "$TMPDESKTOP/io.github.one337.murmel.desktop" 2>&1 &&
  desktop-file-validate "$TMPDESKTOP/io.github.one337.murmel.desktop" 2>&1; then
  pass "desktop file (as tools/install.sh builds it) is valid"
else
  fail "desktop file is invalid"
fi
rm -rf "$TMPDESKTOP"

step "metainfo: appstreamcli validate --no-net"
if appstreamcli validate --no-net packaging/io.github.one337.murmel.metainfo.xml 2>&1; then
  pass "metainfo is valid"
else
  fail "metainfo is invalid"
fi

step "flatpak: local manifest and Flathub template agree"
if go run ./tools/manifestcheck packaging/io.github.one337.murmel.json \
  packaging/flathub/io.github.one337.murmel.json 2>&1; then
  pass "finish-args, runtime, runtime-version, sdk and sdk-extensions match"
else
  fail "the Flatpak manifests differ (keep packaging/flathub/ in line)"
fi

printf '========================\n'
if [ "$failures" -eq 0 ]; then
  printf 'ALL CHECKS PASSED\n'
  exit 0
else
  printf '%d CHECK(S) FAILED\n' "$failures"
  exit 1
fi
