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

step "gofmt -l ."
if gofmt_output="$(gofmt -l . 2>&1)" && [ -z "$gofmt_output" ]; then
  pass "gofmt (no unformatted files)"
else
  printf '%s\n' "$gofmt_output"
  fail "gofmt (files above need formatting)"
fi

step "go vet ./..."
if go vet ./... 2>&1; then
  pass "go vet"
else
  fail "go vet"
fi

step "go build ./..."
if go build ./... 2>&1; then
  pass "go build"
else
  fail "go build"
fi

step "go test ./..."
if go test ./... 2>&1; then
  pass "go test"
else
  fail "go test"
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
xgettext \
  --language=Go \
  --keyword=tr \
  --package-name="Murmel" \
  --package-version="0.1" \
  --copyright-holder="Murmel Contributors" \
  --msgid-bugs-address="https://github.com/one337/murmel/issues" \
  -o "$TMPOT" \
  $(find internal/ui -name '*.go' ! -name '*_test.go' | LC_ALL=C sort) 2>/dev/null
# Strip volatile header lines before diffing
strip_header() { grep -v "^\"POT-Creation-Date\|^\"PO-Revision-Date\|^# FIRST AUTHOR" "$1"; }
if diff <(strip_header po/murmel.pot) <(strip_header "$TMPOT") >/dev/null 2>&1; then
  pass "po/murmel.pot is current"
else
  printf 'Run: xgettext --language=Go --keyword=tr -o po/murmel.pot $(find internal/ui -name '"'"'*.go'"'"' ! -name '"'"'*_test.go'"'"' | LC_ALL=C sort)\n'
  fail "po/murmel.pot is out of date"
fi
rm -f "$TMPOT"

printf '========================\n'
if [ "$failures" -eq 0 ]; then
  printf 'ALL CHECKS PASSED\n'
  exit 0
else
  printf '%d CHECK(S) FAILED\n' "$failures"
  exit 1
fi
