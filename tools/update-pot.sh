#!/usr/bin/env bash
# update-pot.sh — regenerate the translation template from the tr() calls
# in internal/ui.
#
# Usage: tools/update-pot.sh [output]   (default: po/murmel.pot)
#
# References name files, not lines, so the template only changes when the
# translatable strings do (tools/verify.sh compares it).
set -euo pipefail
cd "$(dirname "$0")/.."
out=${1:-po/murmel.pot}
xgettext \
  --language=Go \
  --keyword=tr \
  --keyword=trn:1,2 \
  --add-comments=TRANSLATORS: \
  --add-location=file \
  --package-name="Murmel" \
  --package-version="0.1" \
  --copyright-holder="Murmel Contributors" \
  --msgid-bugs-address="https://github.com/one337/murmel/issues" \
  -o "$out" \
  $(find internal/ui -name '*.go' ! -name '*_test.go' | LC_ALL=C sort) 2>/dev/null
