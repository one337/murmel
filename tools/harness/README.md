# Murmel Test Harness

QA infrastructure for Murmel: a pinned Murmur 1.5 Docker server, headless
gumble bot peers (ping / channel-setup / text+voice), and an Xvfb screenshot
runner for GTK UI tests.

## Layout

```
tools/harness/
├── compose.yaml       pinned Murmur 1.5 server (Docker Compose)
├── murmur.ini         server config, mounted read-only into the container
├── xvfb-run-ui.sh     headless UI runner + screenshot
├── README.md          this file
└── bots/              SEPARATE Go module (io.github/murmel/harness-bots)
    ├── pingbot/       UDP ping probe → JSON
    ├── setupbot/      idempotent channel bootstrap (Lobby, TestChan)
    └── peerbot/       scriptable peer: text, synthetic Opus voice, expectations
```

The bots live in their own Go module so their dependencies (upstream
`layeh.com/gumble`, `layeh.com/gopus` cgo) never pollute the main
`io.github/murmel` module. The root `go build ./...` ignores nested modules
automatically.

## Image pin

| | |
|---|---|
| Image | `ghcr.io/mumble-voip/mumble-server` |
| Tag | `v1.5.939` (latest stable 1.5.x release, published 2026-10-11) |
| Digest | `sha256:2373aaf071dfcea95b0d45bc2ddf6c10318674a313a7e4816e826e9ba4bca942` |
| Resolved | 2026-11-04 via `docker pull` + `docker images --digests` |

The digest is recorded as a comment above the `image:` key in `compose.yaml`.
To re-pin: `docker pull ghcr.io/mumble-voip/mumble-server:vX.Y.Z`,
copy the digest from `docker images --digests`, update both this table and the
compose.yaml comment.

## Server configuration

`murmur.ini` is mounted read-only at `/etc/mumble/murmel.ini` and selected
via `MUMBLE_CUSTOM_CONFIG_FILE` (while set, the image ignores all
`MUMBLE_CONFIG_*` environment variables and uses the file as-is).

Verified gotchas (murmur 1.5 / mumble-server image):

- There is **no `servername` ini key** — the name clients see is
  `registerName` (the root channel name). The harness sets
  `registerName=MurmelQA`.
- There is **no `superuser_password` ini key** — the SuperUser password is set
  via the `MUMBLE_SUPERUSER_PASSWORD` environment variable; the image
  entrypoint runs `mumble-server -supw <value>` on every container start.
- `opusthreshold=0` makes the server use Opus for every voice transmission.
- `autobanAttempts=0` disables autobanning (QA bots reconnect in bursts).
- The SuperUser password is the fixed QA value **`murmelqa`**.
- The server uses an auto-generated self-signed certificate; all bots connect
  with `InsecureSkipVerify` (TOFU is the app's job, not the harness's).

## Run order

```bash
# 1. Start the server (from the repo root)
docker compose -f tools/harness/compose.yaml up -d

# 2. Wait for the healthcheck to pass (~3-5s; cert generation on first start)
docker inspect --format '{{.State.Health.Status}}' murmel-murmur   # → healthy

# 3. Bootstrap channels (idempotent — safe to re-run, never duplicates)
go run ./setupbot   # from tools/harness/bots/

# 4. Probe / test
go run ./pingbot  --addr localhost:64738
go run ./peerbot  --addr localhost:64738 --channel Lobby --speak-seconds 3
```

Tear down (do this after QA runs so the environment stays clean):

```bash
docker compose -f tools/harness/compose.yaml down   # add -v to wipe the DB
```

## Bots

All bots print a single JSON object to stdout and never hang: every network
wait has a timeout. Run them from `tools/harness/bots/` (own module).

### pingbot

```bash
go run ./pingbot --addr localhost:64738 [--timeout 5s]
```

UDP-pings the server via `gumble.Ping` and prints:

```json
{
  "ok": true,
  "address": "127.0.0.1:64738",
  "version": "1.5.939",
  "version_raw": 139841,
  "users": 0,
  "max_users": 50,
  "max_bitrate": 128000,
  "latency_ms": 0.31
}
```

Exit codes: `0` success · `2` failure (timeout/unreachable) with a JSON
`{"ok": false, "error": "..."}` object — no panic, no hang.

### setupbot

```bash
go run ./setupbot [--addr localhost:64738] [--password murmelqa]
```

Connects as **SuperUser** (permanent channel creation needs admin rights on a
default murmur) and creates `Lobby` and `TestChan` under the root channel if
missing. Prints `{"ok": true, "created": [...], "existed": [...]}`.
Idempotent: existing channels are reported in `existed` and left untouched.

### peerbot

```bash
go run ./peerbot [flags]
```

| Flag | Meaning |
|---|---|
| `--addr host:port` | server address (default `localhost:64738`) |
| `--name NAME` | username (default `peerbot-<pid>`; must be unique per server) |
| `--channel NAME` | channel to join after connect (default `Lobby`) |
| `--send-text MSG` | send MSG to the channel after joining |
| `--speak-seconds N` | send a synthetic 440 Hz sine (48 kHz mono, 10 ms/480-sample buffers) as Opus voice for N seconds |
| `--listen-seconds N` | listen for N seconds; counts decoded voice packets per user and records text messages |
| `--expect-text SUBSTR` | fail (exit 1) unless some received text contains SUBSTR within the listen window |
| `--expect-nothing` | fail (exit 1) if any text message is received during the listen window |
| `--expect-voice-from NAME` | fail (exit 1) unless voice packets were decoded from user NAME |
| `--cert FILE` / `--key FILE` | optional client certificate (PEM); `--key` defaults to `--cert`. Create a throwaway one with `openssl req -x509 -newkey rsa:2048 -nodes -days 365 -subj /CN=murmel-test -keyout client.pem -out client.pem` (never commit it) |
| `--timeout DURATION` | connect timeout (default 15s) |

`--expect-text`, `--expect-nothing` and `--expect-voice-from` require
`--listen-seconds > 0` (exit 2 on misuse). Speaking and listening overlap when
both are given, so a single peerbot can talk and listen simultaneously.

Summary JSON (exit `0` only if all expectations were met, else `1`):

```json
{
  "ok": true,
  "name": "listener",
  "channel": "Lobby",
  "joined": true,
  "speak_seconds": 0,
  "buffers_sent": 0,
  "listen_seconds": 5,
  "decoded_packets": 291,
  "decoded_samples": 139680,
  "decoded_by_user": {"speaker": 291},
  "texts_received": [{"sender": "sender", "message": "hello"}],
  "expect_text": "hello",
  "expect_text_met": true
}
```

### Voice codec note

The bots register Opus via `layeh.com/gumble/opus`, which uses `layeh.com/gopus`
(cgo with an **embedded Opus 1.1.2** source tree — no system libopus needed).
Verified to build on this machine (gcc + cgo, no `libopus-devel` installed).
If a future toolchain breaks the cgo build, degrade the bots to text-only by
dropping the blank opus import and document it here loudly.

### Example: two-bot voice + text roundtrip

```bash
cd tools/harness/bots

# Voice: speaker talks 3s, listener counts decoded packets
go run ./peerbot --name listener --listen-seconds 5 --expect-nothing \
  > /tmp/listener.json &
sleep 1
go run ./peerbot --name speaker --speak-seconds 3
wait
grep decoded_packets /tmp/listener.json   # must be > 0

# Text: sender sends, listener asserts receipt
go run ./peerbot --name t-listener --listen-seconds 5 --expect-text 'interop-hello' \
  > /tmp/t-listener.json &
sleep 1
go run ./peerbot --name t-sender --send-text 'interop-hello from sender'
wait
```

## Xvfb UI runner

```bash
bash tools/harness/xvfb-run-ui.sh <binary> <screenshot.png> [lifetime_seconds]
```

Starts `Xvfb :99` (1280x800x24), runs the binary with `DISPLAY=:99` in its own
session, waits `lifetime` seconds (default 5), screenshots the root window via
ImageMagick `import`, kills the process group, and checks the PNG is > 10 KB.

- `USE_EXISTING_DISPLAY=1` reuses `$DISPLAY` instead of starting Xvfb
  (for machines that have a real display).
- If `Xvfb` is missing the script prints an install hint
  (`sudo dnf install xorg-server-xvfb`) and exits **3**; missing ImageMagick
  `import` also exits 3.
- Exit codes: `0` OK · `1` crash/blank screenshot · `2` usage · `3` missing tool.
- The launched binary runs with `GDK_BACKEND=x11` and an empty
  `WAYLAND_DISPLAY` (fix for ISSUE-002: on Wayland hosts the inherited
  `WAYLAND_DISPLAY` made GTK apps attach to the host compositor instead of
  the Xvfb screen, producing 263-byte blank screenshots). No window manager
  is needed — GTK4/libadwaita draw their own decorations.

**First cgo build is slow**: compiling the gotk4/gotk4-adwaita cgo packages
from a cold cache took ~9.5 min on this machine (see `docs/gtk-versions.md`);
warm rebuilds are ~2 s. QA scripts that `go build` UI binaries should not
assume sub-minute builds after a cache invalidation.

**Xvfb**: `xvfb-run-ui.sh` needs `Xvfb` and ImageMagick (`import`); it exits
with code 3 when either is missing.

## Build prerequisite: system libopus (main module)

Unlike the bots (which use gopus's **embedded** Opus 1.1.2), the main
`io.github/murmel` module wraps `gopkg.in/hraban/opus.v2`, which links
against the **system** libopus via cgo. Building or testing
`internal/audio/opus` (and everything importing it) requires:

```bash
pkg-config --exists opus   # verify — prints nothing when satisfied
# Fedora:     sudo dnf install opus-devel
# Debian/Ubuntu: sudo apt install libopus-dev
```

Verified on this machine: pkg-config `opus` 1.6 present, tests pass.

Note: hraban/opus.v2 upstream also requires pkg-config `opusfile`
(libopusfile headers) for its OGG stream API, which this project does not
use and which is NOT installed here. The root `go.mod` therefore redirects
the module to the verbatim trimmed copy in `third_party/hraban-opus/` that
excludes only the opusfile-backed files — see that directory's README.
The harness bots are unaffected (separate module, gopus embeds Opus).
The Flatpak build uses the libopus shipped in the GNOME runtime.
