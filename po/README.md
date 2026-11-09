# Translations

Murmel's user interface is written in English and translated with gettext.
Translations are welcome as pull requests — no account anywhere else is
needed.

## Files

| File | Purpose |
|------|---------|
| `murmel.pot` | Template with every translatable message, generated from the source |
| `<lang>.po` | One translation per language, e.g. `de.po`, `pt_BR.po` |
| `LINGUAS` | The languages that are built into the app |

## Adding a translation

1. Create the catalog from the template, e.g. for German:

   ```sh
   msginit -l de_DE.UTF-8 -i po/murmel.pot -o po/de.po
   ```

   (or copy `murmel.pot` to `de.po` and fill in the header by hand).
2. Translate the `msgstr` entries, with a text editor or a gettext editor
   such as Poedit or GNOME Translation Editor. Comments starting with `#.`
   explain short or ambiguous messages. Keep placeholders like `%s` and
   `%d`; to change their order use `%[2]s` style indexes.
3. Add the language code (`de`) on its own line to `po/LINGUAS`.
4. Check it: `tools/install-translations.sh` must pass (it runs
   `msgfmt --check`, as CI does).
5. Open a pull request with `po/de.po` and `po/LINGUAS`.

## Testing a translation

See your changes in the app without building the Flatpak: compile the
translations into a scratch directory and point Murmel at it.

```sh
tools/install-translations.sh /tmp/murmel-locale
MURMEL_LOCALEDIR=/tmp/murmel-locale LANG=de_DE.UTF-8 go run -mod=vendor ./cmd/murmel
```

`MURMEL_LOCALEDIR` is checked before the installed translations; without
it Murmel uses only those.

## Updating a translation

`tools/update-pot.sh` regenerates the template and merges it into every
`po/*.po`: new messages are added empty, changed ones keep their old
translation marked `fuzzy`, removed ones are kept at the end as `#~`.
Translate what is empty or `fuzzy` (and remove the `fuzzy` mark); until
then those messages show in English. `tools/verify.sh` prints how many
messages each language still lacks.

## For developers

- Wrap user-visible text in `tr("…")`; a number that decides the grammar
  goes through `trn("%d item", "%d items", n)` (in packages that have it).
- Write whole sentences with placeholders instead of joining pieces, and
  put a `// TRANSLATORS: …` comment above messages that need context.
- Regenerate the template with `tools/update-pot.sh` (CI fails when it is
  out of date).

Users can pick the language in Preferences → General (it applies after
a restart). Otherwise the app picks the first of the user's languages
(`LANGUAGE`, `LC_ALL`, `LC_MESSAGES`, `LANG`) that has a translation,
falling back from a regional variant (`de_AT`) to the language (`de`),
and English when there is none. Translations are loaded from
`/app/share/locale` (Flatpak) or `/usr/share/locale`.
