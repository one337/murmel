#!/usr/bin/env bash
# install-translations.sh — compile the translations listed in po/LINGUAS.
#
# Usage: tools/install-translations.sh [destdir]
#
# Installs <destdir>/<lang>/LC_MESSAGES/murmel.mo for every language in
# po/LINGUAS; without destdir it only checks that each po/<lang>.po
# compiles (msgfmt --check), as tools/verify.sh does.
set -euo pipefail
cd "$(dirname "$0")/.."
dest=${1:-}
langs=$(grep -v '^\s*#' po/LINGUAS | tr -s ' \t' '\n' | sed '/^$/d' || true)
for lang in $langs; do
  po="po/$lang.po"
  [ -f "$po" ] || { echo "install-translations: $po listed in po/LINGUAS but missing" >&2; exit 1; }
  if [ -n "$dest" ]; then
    install -d "$dest/$lang/LC_MESSAGES"
    msgfmt --check -o "$dest/$lang/LC_MESSAGES/murmel.mo" "$po"
  else
    msgfmt --check -o /dev/null "$po"
  fi
done
