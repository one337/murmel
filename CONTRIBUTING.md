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
  widget or one of its ancestors strongly, not even through a struct.
  gotk4 keeps the closure as long as the object lives, and the closure
  keeps the object alive, so neither is ever freed. Capture an ID and look
  the object up when the closure runs, or use `internal/ui/weakref`:
  - `weakref.New(w)` for a single widget or other object: the closure
    calls `self.Do(func(w *gtk.Scale) { … })`, which does nothing once `w`
    is gone.
  - `weakref.Owned(dialog, s)` for an owner struct that builds a dialog
    (or another widget) and holds its widgets: `s` then lives exactly as
    long as the dialog, and the closures reach it through the returned
    weak pointer and do nothing when it is nil. `s` holds the dialog
    itself only through `weakref.New`.

  Owners that live as long as the main window (the channel tree, the chat
  view) are the exception: they are never freed anyway. Why a closure that
  holds only its own object is freed while one on a child that holds the
  parent is not (as measured): the first cycle runs only through gotk4's
  toggle reference, which turns weak once GTK no longer holds the object;
  in the second, GTK keeps the child alive through its parent, so gotk4
  keeps the closure and the closure keeps the parent. The rule above
  covers both. The leak tests are named `TestGTK…` and run under
  `xvfb-run` in `tools/verify.sh` (`MURMEL_GTK_TESTS=1`); without a display
  they are skipped, except in CI, where that is a failure.

By contributing you agree that your contribution is licensed under the
GPL-3.0-or-later, like Murmel itself.
