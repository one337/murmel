# Translations

This directory contains the gettext catalog for Murmel.

## Files

| File | Purpose |
|------|---------|
| `murmel.pot` | Master template — regenerated from source |

No `.po` translation files ship yet; English is the only locale for now.

## Regenerating the template

Run from the repository root:

```sh
xgettext \
  --language=Go \
  --keyword=tr \
  --package-name="Murmel" \
  --package-version="0.1" \
  --copyright-holder="Murmel Contributors" \
  --msgid-bugs-address="https://github.com/murmel/murmel/issues" \
  -o po/murmel.pot \
  $(find internal/ui -name '*.go' ! -name '*_test.go')
```

Or run `tools/verify.sh`, which includes a freshness check.

## Adding a translation

1. Copy `murmel.pot` to `po/<lang>.po` (e.g. `de.po`).
2. Fill in the `msgstr` entries.
3. Compile to MO: `msgfmt -o po/<lang>.mo po/<lang>.po`
4. Install to `share/locale/<lang>/LC_MESSAGES/murmel.mo`.

The app loads translations from `/app/share/locale` (Flatpak) or
`/usr/share/locale` (system install) at startup, based on `LANGUAGE`/`LANG`.
