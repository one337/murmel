# GTK stack versions

Working, verified combination for the Murmel UI (validated 2026-11-04, Task 4).

## Go modules

| Module | Version |
|---|---|
| `github.com/diamondburned/gotk4/pkg` | `v0.4.2-0.20261031093702-169e0f82bca8` |
| `github.com/diamondburned/gotk4-adwaita/pkg` | `v0.0.0-20261031204147-41c057c79b39` |

Both resolved by `go mod tidy` / MVS — no explicit pins needed. The two
pseudo-versions are both cut from `master` on 2026-10-31 and are mutually
compatible (the adwaita module's `go.mod` requires the gotk4 `pkg` submodule,
not the repo root).

Note the module layout: current gotk4/adwaita expose their packages via
nested `/pkg` submodules (`gotk4/pkg`, `gotk4-adwaita/pkg`), not the repo
roots. Import paths are unchanged (`.../gotk4/pkg/gtk/v4`,
`.../gotk4-adwaita/pkg/adw`).

## System libraries (cgo, via pkg-config)

| Library | Version |
|---|---|
| `gtk4` | 4.22.5 |
| `libadwaita-1` | 1.9.4 |

## API notes for this pair

- `adw.Application` embeds `gtk.Application` **by value**: pass
  `&app.Application` where a `*gtk.Application` is expected (e.g.
  `adw.NewApplicationWindow`).
- `ConnectActivate` takes a `func()` — capture the app in a closure.
- GTK setter naming is Go-cased: `SetVAlign`, not `SetValign`.
- `glib.IdleAdd` callbacks return `bool`: `false` removes the idle source
  (one-shot), `true` keeps it queued.

## Build duration (cgo)

First build of `cmd/murmel` after `go get` (cold Go build cache for the
gotk4 packages, modules freshly downloaded):

```
real 9m32,736s   user 20m35,792s   sys 2m20,447s
```

Warm rebuild with everything in the build cache: **~1.6 s**.

Expect the first cgo compile after any cache invalidation (gotk4 version
bump, toolchain upgrade, `go clean -cache`) to take minutes — the harness
README documents implications for QA scripts.

## Headless rendering gotcha

On Wayland hosts, `WAYLAND_DISPLAY` leaks into test harnesses; GTK then
attaches to the host compositor instead of Xvfb, producing blank 263-byte
screenshots. The harness now forces `GDK_BACKEND=x11` and clears
`WAYLAND_DISPLAY` for the launched binary (see
`tools/harness/xvfb-run-ui.sh`, formerly ISSUE-002).
