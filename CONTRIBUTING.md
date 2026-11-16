# Contributing to Murmel

Thanks for helping! Bug reports, ideas, translations and code are all
welcome.

- **Bugs and ideas:** open an issue; the templates ask for what helps most
  (Murmel and server version, steps, log output).
- **Translations:** see [po/README.md](po/README.md). Translating needs no
  programming: a `.po` file in a pull request is enough.
- **Code:** see "Building from source" and "Development" in the
  [README](README.md). Before a pull request, run `tools/verify.sh`; CI runs
  the same checks. Commit messages follow
  [Conventional Commits](https://www.conventionalcommits.org/)
  (`feat(chat): …`, `fix(audio): …`).
- **Changes to the gumble fork** (`internal/protocol/gumble`) get an entry
  in [PATCHES.md](PATCHES.md).
- **Signal closures and memory:** a closure attached to a widget (or to
  one of its event controllers, actions or descendants) must not hold that
  widget or one of its ancestors strongly. gotk4 keeps the closure as long
  as the object lives, and the closure keeps the object alive, so neither
  is ever freed. Capture an ID and look the object up when the closure
  runs, or a weak reference from `internal/ui/weakref`
  (`self := weakref.New(w)`, then `self.Do(func(w *gtk.Scale) { … })`).
  Owners that live as long as the main window (the channel tree, the chat
  view) are the exception: they are never freed anyway. The leak tests run
  under `xvfb-run` in `tools/verify.sh` (`MURMEL_GTK_TESTS=1`).

By contributing you agree that your contribution is licensed under the
GPL-3.0-or-later, like Murmel itself.
