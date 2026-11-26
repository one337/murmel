#!/usr/bin/env bash
set -euo pipefail

cd "$(dirname "$0")/.."

failures=0
skipped=0

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

skip() {
  printf 'SKIP: %s\n\n' "$1"
  skipped=$((skipped + 1))
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

# Linters and their exceptions: .golangci.yml. Only the pinned version
# runs, as other versions report differently; the CI runs it in a step of
# its own (.github/workflows/verify.yml).
GOLANGCI_LINT_VERSION=2.15.0
step "golangci-lint $GOLANGCI_LINT_VERSION"
lint_found=$(golangci-lint version --short 2>/dev/null || true)
if [ -n "${CI:-}" ]; then
  skip "golangci-lint (runs in its own step of the CI workflow)"
elif [ "$lint_found" != "$GOLANGCI_LINT_VERSION" ]; then
  printf 'golangci-lint %s is needed, found: %s\n' "$GOLANGCI_LINT_VERSION" "${lint_found:-not installed}"
  printf 'Install the release binary: https://github.com/golangci/golangci-lint/releases/tag/v%s\n' "$GOLANGCI_LINT_VERSION"
  skip "golangci-lint (version $GOLANGCI_LINT_VERSION not installed)"
else
  # golangci-lint takes directories, not import paths.
  mapfile -t LINT_DIRS < <(go list -f '{{.Dir}}' ./... | grep -v '/target/' | sed "s|^$PWD|.|")
  if golangci-lint run "${LINT_DIRS[@]}" 2>&1; then
    pass "golangci-lint"
  else
    fail "golangci-lint (fix the findings or add a reasoned exception)"
  fi
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
done < <(go list -deps -f '{{with .Module}}{{.Path}}{{end}}' ./cmd/murmel | sort -u | grep -vx 'github.com/one337/murmel')
while read -r f; do
  [ -f "$f" ] || lic_problems+=("missing license file $f")
done < <(awk '!/^#/ && NF >= 3 { for (i = 3; i <= NF; i++) print $i }' tools/licenses.txt)
if [ ${#lic_problems[@]} -eq 0 ]; then
  pass "licenses"
else
  printf '  %s\n' "${lic_problems[@]}"
  fail "licenses"
fi

# The linker ignores -X for a variable that does not exist, so a stale
# path only shows up as a wrong version in the About dialog. Every build
# description must name a variable of this module, and in a binary built
# with those flags (as tools/install.sh does, without -trimpath to reuse
# the build cache) the variable must hold the value passed in. The value
# is read from the string the linker stores for -X (<variable>.str): the
# flags themselves are also recorded in the binary's build information.
step "version: the -X flags of every build reach the binary"
x_problems=()
x_targets=()
x_module=$(go list -m)
for f in tools/install.sh packaging/nix/package.nix packaging/rpm/murmel.spec.in README.md; do
  mapfile -t x_found < <(grep -o -- '-X [^ =]*=' "$f" | sed 's/^-X //; s/=$//')
  [ ${#x_found[@]} -gt 0 ] || x_problems+=("$f: no -X flag")
  for t in "${x_found[@]}"; do
    x_targets+=("$f $t")
  done
done
x_marker="verify-version-$$"
x_ldflags=""
for ft in "${x_targets[@]}"; do
  f=${ft%% *} t=${ft#* }
  if [ "$(go list -f '{{with .Module}}{{.Path}}{{end}}' "${t%.*}" 2>/dev/null)" != "$x_module" ]; then
    x_problems+=("$f: -X $t: ${t%.*} is no package of $x_module")
  fi
  x_ldflags+=" -X $t=$x_marker"
done
x_dir=$(mktemp -d)
if go build -mod=vendor -ldflags "$x_ldflags" -o "$x_dir/murmel" ./cmd/murmel 2>&1; then
  x_syms=$(go tool nm "$x_dir/murmel")
  for ft in "${x_targets[@]}"; do
    f=${ft%% *} t=${ft#* }
    x_addr=$(awk -v s="$t.str" '$NF == s { print $1 }' <<<"$x_syms")
    if [ -z "$x_addr" ]; then
      x_problems+=("$f: -X $t: the linker set no such variable")
      continue
    fi
    x_hex=$(LC_ALL=C objdump -s --start-address="0x$x_addr" \
      --stop-address=$((0x$x_addr + ${#x_marker})) "$x_dir/murmel" |
      awk '/^ [0-9a-f]+ / { print substr($0, length($1) + 3, 35) }' | tr -d ' \n')
    x_value=$(printf '%b' "$(sed 's/../\\x&/g' <<<"$x_hex")")
    [ "$x_value" = "$x_marker" ] ||
      x_problems+=("$f: -X $t: the binary holds \"$x_value\", not \"$x_marker\"")
  done
else
  x_problems+=("go build with the -X flags failed")
fi
rm -rf "$x_dir"
if [ ${#x_problems[@]} -eq 0 ]; then
  pass "version (${#x_targets[@]} -X flags checked)"
else
  printf '  %s\n' "${x_problems[@]}"
  fail "version"
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
GTK_TEST_PKGS=(./internal/ui ./internal/ui/channeltree ./internal/ui/chat ./internal/ui/admin ./internal/ui/connection ./internal/ui/dragicon ./internal/ui/identities ./internal/ui/prefs ./internal/ui/richtext)
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
      MESA_SHADER_CACHE_DISABLE=true G_DEBUG=fatal-criticals \
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
  fail "hardcoded strings found (wrap them in i18n.Tr())"
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

step "i18n: translations are complete"
# An untranslated or fuzzy message shows in English.
incomplete=()
for po in po/*.po; do
  [ -e "$po" ] || continue
  stats=$(LC_ALL=C msgfmt --statistics -o /dev/null "$po" 2>&1)
  printf '  %s: %s\n' "$(basename "$po" .po)" "$stats"
  case $stats in
  *untranslated* | *fuzzy*) incomplete+=("$po") ;;
  esac
done
if [ ${#incomplete[@]} -eq 0 ]; then
  pass "every message is translated"
else
  printf 'List them: msgattrib --untranslated <po>; msgattrib --only-fuzzy <po>\n'
  fail "untranslated or fuzzy messages in ${incomplete[*]}"
fi

step "i18n: translations keep the format arguments"
if go test ./internal/ui -run 'TestTranslationsKeepTheFormatArguments|TestFormatArgs|TestFormatMismatches' 2>&1; then
  pass "every translation uses the format arguments of its original"
else
  fail "a translation lost or gained a format argument (see above)"
fi

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
if [ "$skipped" -gt 0 ]; then
  printf '%d CHECK(S) SKIPPED (see SKIP above)\n' "$skipped"
fi
if [ "$failures" -eq 0 ]; then
  printf 'ALL CHECKS PASSED\n'
  exit 0
else
  printf '%d CHECK(S) FAILED\n' "$failures"
  exit 1
fi
