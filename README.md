<div align="center">

<h1><img src="assets/murmel.svg" alt="" width="40" height="40" align="top">&nbsp;Murmel</h1>

<p><strong>A <a href="https://www.mumble.info/">Mumble</a> voice chat client for GNOME</strong><br>
written in Go with GTK&nbsp;4 and Libadwaita</p>

<p>
  <a href="https://simon-tschoep.de/software/murmel/">Website</a>&nbsp;·
  <a href="#install">Install</a>&nbsp;·
  <a href="#building-from-source">Build</a>&nbsp;·
  <a href="CONTRIBUTING.md">Contribute</a>
</p>

<p>
  <a href="https://github.com/one337/murmel/actions/workflows/verify.yml"><img src="https://github.com/one337/murmel/actions/workflows/verify.yml/badge.svg" alt="CI"></a>
  <a href="LICENSE"><img src="https://img.shields.io/badge/license-GPL--3.0--or--later-blue" alt="License: GPL-3.0-or-later"></a>
</p>

<img src="assets/screenshots/0.3.7/light-dark-mode.webp" width="88%" alt="Murmel connected to a server: channel tree with users and avatars, chat with an image, switching between the light and the dark style">

</div>

## Features

- **Native GNOME app** – modern GTK 4 and Libadwaita, adaptive from phone
  to desktop, light and dark style; available in English and German, with
  more languages to come ([contributions welcome](po/README.md))
- **Clear voice** – Opus over encrypted UDP with TCP fallback, noise
  suppression and echo cancellation, voice activation with a live level
  meter
- **Chat** – channel and private messages with formatting, emoji and
  images; unread markers, notifications and configurable event sounds
- **Channels at a glance** – channel tree with avatars and speaking
  indicators, descriptions with cover images, drag and drop support
- **Connections and identities** – save servers and sort them as you like,
  passwords stored securely, `mumble://` links; create, import and back up
  your certificates right in the app
- **Server administration** – edit channels with groups and permissions,
  kick, ban and manage registered users

## Screenshots

<table>
  <tr>
    <td width="50%"><img src="assets/screenshots/0.3.7/light-mode.png" alt="Channel chat in the light style"></td>
    <td width="50%"><img src="assets/screenshots/0.3.7/dark-mode.png" alt="Channel chat in the dark style"></td>
  </tr>
  <tr>
    <td align="center"><sub>Light style</sub></td>
    <td align="center"><sub>Dark style</sub></td>
  </tr>
  <tr>
    <td width="50%"><img src="assets/screenshots/0.3.7/channel-coverimage.png" alt="A chess channel whose description starts with a wide image, shown as a banner behind the header"></td>
    <td width="50%"><img src="assets/screenshots/0.3.7/settings.png" alt="Preferences: language and appearance, channel log events, and audio input with noise suppression, echo cancellation and the voice activation threshold"></td>
  </tr>
  <tr>
    <td align="center"><sub>Channel with a cover image</sub></td>
    <td align="center"><sub>Preferences</sub></td>
  </tr>
</table>

Murmel follows the desktop's light or dark style, or keeps the one you pick
in Preferences. A channel description that starts with a wide image becomes
the channel's cover image.

## Install

Murmel is distributed as a Flatpak from its own repository. Only Flatpak
itself is needed; the GNOME runtime comes from Flathub, which Flatpak offers
to add during the installation if it is not set up yet:

```sh
flatpak install --user https://simon-tschoep.de/software/murmel/io.github.murmel.Murmel.flatpakref
flatpak run io.github.murmel.Murmel
```

Updates then arrive through GNOME Software or `flatpak update`.

> [!NOTE]
> If Flatpak was not installed before, you may have to restart the computer
> before Murmel starts from the app grid.

Server passwords: the Flatpak keeps them in its own encrypted storage,
with a key from the desktop's Secret portal (shown as "Secret Portal
Token" in the keyring); RPM and source installs keep them in the desktop
keyring.

On Fedora, Murmel is also available as an RPM from
[Copr](https://copr.fedorainfracloud.org/coprs/one337/murmel/):

```sh
sudo dnf copr enable one337/murmel
sudo dnf install murmel
```

## Building from source

Murmel needs a current GNOME stack (GTK 4 and Libadwaita 1.10, as in
Fedora 45 or GNOME 51), Go 1.27 or newer and WebRTC audio processing 2.x.
Where the distribution is older, the [Flatpak](#flatpak) build is the
easier way. At runtime it uses PulseAudio or PipeWire (with
`pipewire-pulse`).

### Fedora

```sh
sudo dnf install golang gcc-c++ gettext gtk4-devel libadwaita-devel \
    gobject-introspection-devel opus-devel webrtc-audio-processing-devel \
    libsecret-devel
tools/install.sh --prefix ~/.local     # or: sudo tools/install.sh
```

### Other distributions

Install the development files of GTK 4, Libadwaita, GObject
Introspection, Opus, libsecret and WebRTC audio processing 2.x
(`webrtc-audio-processing-2` in pkg-config), plus Go, a C++20 compiler and
gettext.

> [!NOTE]
> Debian and Ubuntu ship only WebRTC audio processing 1.x so far; build 2.x
> from [its source](https://gitlab.freedesktop.org/pulseaudio/webrtc-audio-processing)
> or use the [Flatpak](#install).

Then:

```sh
tools/install.sh --prefix ~/.local     # or: sudo tools/install.sh
```

`tools/install.sh` builds Murmel and installs it with the settings schema,
desktop file, icons, D-Bus service and translations, to `/usr/local` by
default or the given `--prefix` (`~/.local` needs no root, but
`~/.local/bin` must be in `PATH`). `tools/install.sh --uninstall` with the
same prefix removes it again. Dependencies are vendored, so the build works
offline; the first one compiles the GTK bindings and takes several minutes.

To only build the binary:

```sh
go build -mod=vendor -ldflags "-X io.github/murmel/internal/ui.Version=$(tools/version.sh)" -o murmel ./cmd/murmel
```

Without the installed settings schema (or `GSETTINGS_SCHEMA_DIR` pointed at
a directory compiled with `glib-compile-schemas data/`) Murmel falls back to
in-memory settings.

### RPM

To build the RPM that Copr builds, on Fedora:

```sh
sudo dnf install rpm-build
make -f .copr/Makefile srpm outdir=build-srpm
sudo dnf builddep build-srpm/murmel-*.src.rpm
rpmbuild --rebuild build-srpm/murmel-*.src.rpm   # result in ~/rpmbuild/RPMS/
```

### Flatpak

```sh
tools/testbuild.sh
```

builds the current tree with `flatpak-builder` and installs it for the
current user as the branch `master`, next to a release install (`stable`),
and makes it the one that starts;
`flatpak make-current --user io.github.murmel.Murmel stable` switches back.
The manifest uses the GNOME 51 runtime and the Go SDK extension from
Flathub.

### Versions

The About dialog shows the version from `tools/version.sh`: `0.3.7` for a
build of the release tag, `0.3.7-dev+5.g0782834` for one 5 commits after
it (at commit `0782834`, `.dirty` with uncommitted changes), and
`0.3.7-dev` where git is not available.

## Development

```sh
tools/verify.sh          # gofmt, vet, build, tests, race detector,
                         # govulncheck, schema and i18n checks
go test -mod=vendor ./...
```

- `internal/protocol/gumble` is a patched fork of
  [layeh/gumble](https://github.com/layeh/gumble); every change is
  documented in [PATCHES.md](PATCHES.md).
- `tools/harness` contains a local Mumble server setup and test bots for
  end-to-end voice tests, see its README.
- UI strings go through `tr()`; regenerate `po/murmel.pot` with
  `tools/update-pot.sh`.
- `tools/sounds/gen.py` synthesizes the built-in event sounds.

### Releases

`tools/release.sh <x.y.z>` bumps the version and release notes, tags the
commit, builds a signed Flatpak repository into `web/` (with
`.flatpakrepo`, `.flatpakref`, a single-file bundle and an index page) and
uploads it to the website (`--upload` repeats only the upload). Settings
are in `tools/release.conf` and an untracked `tools/release.local.conf`
(upload target); the signing key is kept outside the repository.

## Contributing

Bug reports, ideas, translations and code are welcome; see
[CONTRIBUTING.md](CONTRIBUTING.md).

## Translations

Murmel is available in English and German; how to add or improve a
translation is described in [po/README.md](po/README.md).

## License

Murmel, including its app icon, is licensed under GPL-3.0-or-later, see
[LICENSE](LICENSE). Murmel is not affiliated with the Mumble project.

### Bundled components

These components are built into Murmel. Their source code is in this
repository (`vendor/`, `third_party/` and `internal/protocol/`), and their
license texts are installed with Murmel, to `share/licenses/murmel` (in the
Flatpak `share/licenses/io.github.murmel.Murmel`); the list is
[tools/licenses.txt](tools/licenses.txt).

| Component | License |
|---|---|
| [gotk4](https://github.com/diamondburned/gotk4) and [gotk4-adwaita](https://github.com/diamondburned/gotk4-adwaita) | MPL-2.0 |
| A fork of [gumble](https://github.com/layeh/gumble) in `internal/protocol`, changes in [PATCHES.md](PATCHES.md) | MPL-2.0 |
| [Mumble](https://www.mumble.info/)'s protocol definitions (`MumbleProto`) and voice encryption (`internal/protocol/crypt`, after Mumble's `CryptStateOCB2`) | BSD-3-Clause |
| [malgo](https://github.com/gen2brain/malgo) with [miniaudio](https://miniaud.io/) | Unlicense |
| [golang/protobuf](https://github.com/golang/protobuf) and [golang.org/x/sync](https://pkg.go.dev/golang.org/x/sync) | BSD-3-Clause |
| [hraban/opus](https://github.com/hraban/opus) Go bindings, in `third_party/hraban-opus` | MIT |
| [oggvorbis](https://github.com/jfreymuth/oggvorbis) and [vorbis](https://github.com/jfreymuth/vorbis) | MIT |
| [gotext](https://github.com/leonelquinteros/gotext) | MIT |
| The two user icons in `data/icons/symbolic`, from the [GNOME Icon Development Kit](https://gitlab.gnome.org/Teams/Design/icon-development-kit) | CC0-1.0 |
