# GTK stack versions

Working, verified combination for the Murmel UI (validated 2026-11-20 on
Fedora 45, as in CI).

## Go modules

| Module | Version |
|---|---|
| `github.com/diamondburned/gotk4/pkg` | `v0.4.2-0.20261031093702-169e0f82bca8` |
| `github.com/diamondburned/gotk4-adwaita/pkg` | `v0.0.0-20261031204147-41c057c79b39` |

Both are pseudo-versions from `master` (2026-10-31); `go.mod` is
authoritative.

## System libraries (cgo, via pkg-config)

| Library | Version |
|---|---|
| `gtk4` | 4.24.1 |
| `libadwaita-1` | 1.10.0 |

GTK 4.22.5 with Libadwaita 1.9.4 (Fedora 44, NixOS 26.05) builds, but three
GTK tests fail there: the user menu in the channel tree and the freeing of
the connect dialog behave differently.

## Build duration (cgo)

The first build with a cold Go build cache takes several minutes (cgo
compile of gotk4); warm rebuilds take a few seconds. Expect the same after a
gotk4 bump, a toolchain upgrade or `go clean -cache`.

## Headless rendering

On Wayland hosts an inherited `WAYLAND_DISPLAY` makes GTK attach to the host
compositor instead of Xvfb, producing blank screenshots.
`tools/harness/xvfb-run-ui.sh` therefore sets `GDK_BACKEND=x11` and clears
`WAYLAND_DISPLAY` for the launched binary.
