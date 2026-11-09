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
mapfile -t GOFILES < <(git ls-files '*.go' ':!:vendor/**')
# Packages without GTK widgets: fast enough for the race detector (the UI
# packages compile gotk4 with -race, which takes many minutes).
mapfile -t RACE_PKGS < <(printf '%s\n' "${PKGS[@]}" | grep -v '/internal/ui')

step "gofmt (tracked files)"
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

step "go test"
if go test "${PKGS[@]}" 2>&1; then
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

printf '========================\n'
if [ "$failures" -eq 0 ]; then
  printf 'ALL CHECKS PASSED\n'
  exit 0
else
  printf '%d CHECK(S) FAILED\n' "$failures"
  exit 1
fi
