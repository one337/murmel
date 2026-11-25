#!/usr/bin/env bash
# update-pot.sh — regenerate the translation template from the i18n.Tr() calls
# in internal/ui and the desktop file.
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
  --keyword=Tr \
  --keyword=TrN:1,2 \
  --add-comments=TRANSLATORS: \
  --add-location=file \
  --package-name="Murmel" \
  --package-version="0.1" \
  --copyright-holder="Murmel Contributors" \
  --msgid-bugs-address="https://github.com/one337/murmel/issues" \
  -o "$out" \
  $(find internal/ui -name '*.go' ! -name '*_test.go' | LC_ALL=C sort) 2>/dev/null

# The desktop file: only Comment and Keywords are translated (the name
# stays Murmel).
xgettext \
  --join-existing \
  --language=Desktop \
  --keyword= --keyword=Comment --keyword=Keywords \
  --add-location=file \
  -o "$out" \
  data/io.github.one337.murmel.desktop.in 2>/dev/null

# Writing the real template also brings every translation up to date:
# new and changed messages are added empty, removed ones become obsolete
# (#~). Existing translations are kept. No fuzzy matching: it fills a new
# message with the translation of a similar one, which is easily taken
# over by mistake (with a format argument the message does not have).
if [ "$out" = "po/murmel.pot" ]; then
  for po in po/*.po; do
    [ -e "$po" ] || continue
    msgmerge --quiet --update --no-fuzzy-matching --backup=none "$po" "$out"
  done
fi
