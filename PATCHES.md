# Gumble Fork Patches

Every patch applied to `internal/protocol` (the vendored fork of [layeh/gumble](https://github.com/layeh/gumble), upstream ref `d1df60a3cc14e2d47e2b46d0d887dc79c2ff64c9`) gets an entry in this file describing what was changed and why.

Vendored packages (import paths mechanically rewritten from
`layeh.com/gumble/...` to `io.github/murmel/internal/protocol/...`):

- `gumble` → `internal/protocol/gumble`
- `gumble/MumbleProto` → `internal/protocol/gumble/MumbleProto`
- `gumble/varint` → `internal/protocol/gumble/varint`
- `gumbleutil` → `internal/protocol/gumbleutil`

Skipped: `gumbleopenal`, `gumbleffmpeg`, `opus` (gopus), `cmd/`, `_examples/`,
`misc/`. Upstream `LICENSE` (MPL-2.0) kept at `internal/protocol/gumble/LICENSE`
and `internal/protocol/gumbleutil/LICENSE`.

Every deviation from upstream lives in exactly one commit documented below
(`git log --oneline internal/protocol/` maps 1:1 to the `## P<n>` sections;
`P0` is the verbatim vendoring itself, `P1`–`P6` are the patches).

---

## P0 — Vendoring: import layeh/gumble d1df60a verbatim

- **Commit**: `chore(vendor): import layeh/gumble d1df60a verbatim`
- **What**: copy of `gumble`, `gumble/MumbleProto`, `gumble/varint`,
  `gumbleutil` from upstream `d1df60a3cc14e2d47e2b46d0d887dc79c2ff64c9`, plus
  upstream `LICENSE` (MPL-2.0). The only textual change is the mechanical
  import-path rewrite (`layeh.com/gumble/gumble` →
  `io.github/murmel/internal/protocol/gumble`, same for `gumbleutil`), done
  with sed; verified via `diff -r` — zero non-import lines changed.
- **Why**: Murmel needs a maintained protocol core we can patch (see P1–P5)
  without dragging upstream's cgo extras (`gopus`, `go-openal`) into the main
  module.
- **Upstream**: https://github.com/layeh/gumble/tree/d1df60a3cc14e2d47e2b46d0d887dc79c2ff64c9

---

## P1 — Replace gopus codec registration with explicit codec seam

- **Commits**: `9937f55` (T8 glue: `feat(protocol): wire audio/opus wrapper
  into fork codec registry`) + this ledger/doc consolidation commit
- **What**: upstream's Opus registration lived in the separate
  `layeh.com/gumble/opus` subpackage — an `init()` registering a gopus-backed
  generator (unmaintained cgo, embedded Opus 1.1.2). That subpackage was not
  vendored. Replacement: `internal/protocol/gumble/audiocodec_opus.go` adds
  `RegisterMurmelOpusCodec()`, an explicit (no init side effect) registration
  of the project Opus wrapper (`internal/audio/opus`, hraban/opus.v2 + system
  libopus) as codec type 4 via the verbatim `RegisterAudioCodec` registry.
  `audiocodec.go` (registry + `AudioCodec`/`AudioEncoder`/`AudioDecoder`
  interfaces) is untouched upstream code; the wrapper satisfies the interfaces
  structurally. This commit additionally folds the interim
  `internal/protocol/gumble/PATCHES.md` ledger (added by 9937f55) into this
  root ledger and points `doc.go`'s codec section at the explicit registration
  instead of the dropped import side effect.
- **Why**: drop the unmaintained gopus dependency from the main module while
  keeping the codec-registry interface stable as the drop-in hook for
  `internal/audio/opus` (T8) and any future codec implementation.
- **Upstream**: https://github.com/layeh/gumble/blob/d1df60a3cc14e2d47e2b46d0d887dc79c2ff64c9/opus/opus.go

---

## P2 — Fix handleUserStats FromClient/FromServer copy-paste bug

- **Commit**: `fix(protocol): correct FromServer stat guards in handleUserStats (P2)`
- **What**: in `handleUserStats` (upstream `handlers.go:1124-1137`), the
  `packet.FromServer != nil` block guarded its Late/Lost/Resync assignments
  with `packet.FromClient.* != nil` (copy-paste). The guards now check
  `packet.FromServer.*`; a nil FromServer stat no longer writes a zero value
  that was never sent (nil-pointer write-through was masked by the mismatched
  guard, silently corrupting UserStats.FromServer).
- **Why**: wrong guard either overwrites real stats with zeros or crashes on a
  genuinely nil `packet.FromServer` field; `stats` subcommand of the interop
  CLI depends on these values being correct.
- **Upstream**: https://github.com/layeh/gumble/blob/d1df60a3cc14e2d47e2b46d0d887dc79c2ff64c9/gumble/handlers.go#L1124-L1137 (no upstream issue filed; bug is obvious from the diff)

---

## P3 — Decode incoming audio off the TCP read loop

- **Commit**: `fix(protocol): decode incoming audio off the TCP read loop (P3)`
- **What**: upstream `handleUDPTunnel` decoded every incoming Opus frame
  synchronously and delivered it to `AudioListener`s over unbuffered
  channels — all on the network read goroutine, so a slow decode (or a
  listener that stalled its `AudioStreamEvent.C` channel) froze every
  protocol message including pings (upstream issue #38, documented at
  `gumble/event.go:10-12`). This patch splits the handler: `handleUDPTunnel`
  now only parses/varints the frame header, copies the payload (the read
  buffer is reused by `Conn.ReadPacket`) and enqueues an `audioJob` onto a
  bounded per-connection queue (`audioDispatchQueueSize` = 256 packets,
  ≈2.5 s of audio). A per-connection goroutine (`audioDispatchRoutine`,
  started in `DialWithDialer`, drained by `close(c.audioQueue)` when
  `readRoutine` exits) owns decoding and listener delivery; the decode and
  delivery code is moved verbatim from `handleUDPTunnel` into
  `audio_dispatch.go:dispatchAudio`. When the queue is full, packets are
  dropped instead of blocking the read loop; `Client.AudioDropped()` counts
  the drops. The codec is resolved on the read loop at enqueue time because
  `c.audioCodec` is written by `handleCodecVersion` on that goroutine. The
  P1/T8 codec seam is unaffected — the registry still routes to
  `internal/audio/opus` via `RegisterMurmelOpusCodec`.
- **Why**: voice decode (cgo libopus) and consumer back-pressure must never
  stall control-channel processing; this is the precondition for the GTK app
  driving listeners from the UI thread.
- **Upstream**: https://github.com/layeh/gumble/issues/38

---

## P4 — Wire positional audio through AudioBuffer.writeAudio

- **Commit**: `fix(protocol): wire positional audio through writeAudio (P4)`
- **What**: upstream `AudioBuffer.writeAudio` (upstream `audio.go:71`) had
  a `// TODO: re-enable positional audio` and always passed `nil, nil, nil`
  to `Conn.WriteAudio`, even though the latter (upstream `conn.go:65`) has
  full positional support — three little-endian float32s appended after the
  Opus payload. This patch threads `X, Y, Z *float32` through `writeAudio`
  into `Conn.WriteAudio` and adds `Client.AudioOutgoingPositional(X, Y, Z)`,
  which behaves exactly like `AudioOutgoing` (which becomes
  `AudioOutgoingPositional(nil, nil, nil)` — wire format unchanged) but
  attaches the captured coordinates to every packet of the stream. Incoming
  positional parsing was already correct (now in `dispatchAudio`); receive
  side exposes it via `AudioPacket.HasPosition/X/Y/Z`.
- **Why**: positional audio (game/VR contexts) is part of the Mumble voice
  packet format and the plan's positional feature track; the send path was
  the missing half.
- **Upstream**: https://github.com/layeh/gumble/blob/d1df60a3cc14e2d47e2b46d0d887dc79c2ff64c9/gumble/audio.go#L71 (TODO in vendored code)

---

## P5 — Close per-user audio streams and decoders on disconnect

- **Commit**: `fix(protocol): close per-user audio streams and decoders on disconnect (P5)`
- **What**: upstream never tore down a user's incoming-audio state when that
  user disconnected: the per-listener `audioEventItem.streams` channel was
  left open forever (listener goroutines ranging `AudioStreamEvent.C` hang,
  and the map entry leaks), and `user.decoder` (upstream TODO "de-reference
  after stream is done" at `handlers.go:106`) kept the cgo Opus decoder
  alive. `handleUserRemove` now enqueues a `gone` audio job; the dispatch
  goroutine — sole owner of stream-channel sends since P3, so closing there
  cannot race an in-flight send — closes the user's channels and clears the
  decoder (`closeUserAudio`). Enqueue is non-blocking on the full queue;
  the dispatch goroutine's exit sweep (run when `readRoutine` closes the
  audio queue at connection teardown) closes any still-open streams, so no
  path leaves a listener hanging. The decoder is cleared before the channel
  close so observers of the closed stream also see `decoder == nil`.
- **Why**: long-running sessions churn users; without this, every user that
  ever spoke leaks a goroutine-pair endpoint, a map entry and an Opus
  decoder for the life of the connection.
- **Upstream**: https://github.com/layeh/gumble/blob/d1df60a3cc14e2d47e2b46d0d887dc79c2ff64c9/gumble/handlers.go#L103-L113 (TODOs in vendored code)

---

## P6 — Guard varint Encode against MinInt64 overflow recursion

- **Commit**: `fix(protocol): guard varint Encode against MinInt64 overflow recursion (P6)`
- **What**: upstream `varint.Encode` (upstream `gumble/varint/write.go`) handles
  negative values with the recursive form `0xF8 + Encode(-value)`. For
  `math.MinInt64`, negation overflows in two's complement (`-MinInt64 ==
  MinInt64`), so the recursion never makes progress: it recursed forever,
  walking one byte deeper per call until it ran off the end of the caller's
  buffer and panicked with index-out-of-range (with a 10-byte buffer, after
  consuming it). The guard emits the magnitude 2^63 directly in the 64-bit
  form: `0xF8 0xF4 0x80 00 00 00 00 00 00 00` — exactly `MaxVarintLen` bytes,
  terminating. `Decode` needed no change: the 64-bit form's unsigned payload
  2^63 wraps to `MinInt64`, and the recursion's negation `-MinInt64` wraps
  back to `MinInt64`, so the encoding roundtrips (pinned by
  `TestEncodeMinInt64Terminates` alongside `MinInt64+1 == -MaxInt64`).
- **Why**: any field that ever carried `MinInt64` (or an attacker-controlled
  varint echoed back) turned an O(1) encode into unbounded recursion →
  buffer/stack exhaustion panic. Found by T6's edge-table tests; no Mumble
  protocol field currently sends negative values, so this is latent rather
  than reachable from a stock server.
- **Proof**: pre-fix, `Encode(buf, math.MinInt64)` panics with
  `index out of range` after exhausting the buffer (each recursion level
  shifts the slice by one: `b[1:]`); post-fix it returns 10 bytes and
  `Decode(Encode(MinInt64)) == MinInt64`. Edge-table rows pin the exact wire
  bytes; the seeded property test (10k values, seed 1) covers every other range.
- **Upstream**: https://github.com/layeh/gumble/blob/d1df60a3cc14e2d47e2b46d0d887dc79c2ff64c9/gumble/varint/write.go (latent upstream bug; single-value overflow guard, wire format unchanged for every other value)

---

## P7 — Implement CryptSetup handling (key exchange + both resync directions)

- **Commit**: `feat(protocol): implement CryptSetup key exchange + resync handling (P7)`
- **What**: upstream `handleCryptSetup` (upstream `handlers.go:898-900`) returned
  `errUnimplementedHandler` — CryptSetup messages were silently dropped (the
  read loop ignores handler errors), so the OCB2 key material the server
  sends after authentication was thrown away and native UDP voice was
  impossible. The new `cryptsetup.go` implements the full official-client
  state machine (`MainWindow::msgCryptSetup`, verified against Murmur 1.5
  `Messages.cpp`/`Server.cpp doSync`):
  full `{key, client_nonce, server_nonce}` → `SetKey` (client_nonce
  encrypts, server_nonce decrypts); `{server_nonce}` only → `SetResync`
  (answer to our resync request); empty or `{client_nonce}` only →
  server-initiated resync: reply with our current encrypt IV, change
  nothing locally. `requestCryptResync()` sends the empty CryptSetup that
  Murmur answers with `{server_nonce: its encrypt IV}`. Key material lives
  in a new mutex-guarded `Client.crypt` (`lockedCrypt`) over
  `internal/protocol/crypt` (T5) — the rolling nonces are touched from the
  read loop, the UDP loops and voice writers concurrently, and
  `crypt.CryptState` is not concurrency-safe.
- **Why**: precondition for native UDP voice (P8); also stops silently
  ignoring a control message every Murmur session receives.
- **Upstream**: https://github.com/layeh/gumble/blob/d1df60a3cc14e2d47e2b46d0d887dc79c2ff64c9/gumble/handlers.go#L898-L900 (unimplemented stub)

---

## P8 — Native UDP voice transport (CryptSetup-driven, with TCP-tunnel fallback)

- **Commit**: `feat(protocol): native UDP voice with CryptSetup + resync + TCP fallback (P8)`
- **What**: upstream gumble never speaks UDP — all voice is tunneled through TCP
  UDPTunnel messages, and its `ping.go` only implements the *unencrypted*
  pre-1.2 crypto-ping. This patch adds `udp.go`: a per-`Client` transport
  (created by `DialWithDialer` when the new `Config.UDPVoice` is set) that
  binds a connected UDP socket to the server address and speaks the legacy
  UDP wire format the client's protocol version advertises. Every datagram
  is OCB2-encrypted through P7's `lockedCrypt` (T5 `crypt.CryptState`,
  keys from the server's CryptSetup). Lifecycle mirrors the official
  client (`ServerHandler.cpp`): once a full CryptSetup installs key
  material the transport probes with encrypted pings (header byte 0x20 +
  varint timestamp) at a ~1 s cadence; the first decrypted datagram
  activates it, after which outgoing voice routes over UDP; no decryptable
  packet within 3 s of start (handshake window) or 5 s while active
  (good window — official client disconnects there, we degrade) →
  permanent fallback to the TCP tunnel; socket write errors fall back
  immediately. Voice is never dropped by the switch: `sendVoicePacket`
  reports false and the caller re-sends the same standalone packet bytes
  through `Conn.WriteAudio`. Persistent decrypt failures (≥
  `crypt.ResyncThreshold`) trigger the P7 resync request (rate-limited to
  one per 5 s). To make both transports carry byte-identical packets,
  `Conn.WriteAudio`'s framing moved into `buildVoicePacket` (the exact
  bytes the T6 golden tests pin), and the UDPTunnel receive handler was
  split into `handleVoicePacket` — the same parse the UDP receive loop
  feeds decrypted datagrams into. Exposed: `Client.VoiceTransport()`
  ("udp"/"tcp") and `Client.UDPVoiceStats()` (pings/voice/resync/fallback
  counters) for QA evidence and later UI display.
- **Why**: UDP is Mumble's primary voice path — TCP-tunneling every 10 ms
  frame adds head-of-line latency and collapses under minor loss; native
  UDP with automatic fallback gives the GNOME client official-client
  transport parity.
- **Proof**: `udp_test.go` runs against a loopback fake-Murmur UDP endpoint
  with the production timing constants: `TestUDPPingPacketCodec` (ping
  format golden + rejects), `TestUDPCryptResync` (8 corrupt datagrams →
  empty CryptSetup request on TCP → `{server_nonce}` answer → next packet
  decrypts; no fallback), `TestUDPFallback` (bound-sink blackhole →
  fallback in ~3 s < 5 s bound → voice continues via UDPTunnel with T6
  golden bytes), `TestUDPVoiceDatagramMatchesTCPTunnel` (UDP datagram
  plaintext == TCP tunnel body, byte-for-byte). Verified live against the
  harness server in both directions (transport=UDP, peerbot decodes
  496/500 buffers).
- **Upstream**: https://github.com/layeh/gumble/blob/d1df60a3cc14e2d47e2b46d0d887dc79c2ff64c9/gumble/ping.go (unencrypted ping only; no CryptState integration, no voice transport)

## P9 — Nil-guard Channel.Permission for client-less channels

**What**: `Channel.Permission()` returns `nil` when the channel has no
attached client instead of dereferencing `c.client.permissions` and
panicking.

**Why**: The documented contract is "nil if the permissions are unknown".
Channels constructed outside a live connection (bus bridge unit tests,
tooling) have no client back-reference and crashed with SIGSEGV
(bus `TestBridgeConnectSeedsTree`). Production clients always set
`channel.client`, so behavior there is unchanged.

**Upstream**: layeh/gumble `gumble/channel.go` (same latent panic);
not reported upstream — dormant since 2019.

## P10 — Return from Dial when the connection closes before sync

**What**: `DialWithDialer` also selects on `client.end` while waiting for
`ServerSync`/`Reject`, returning an error when the read loop exits first.

**Why**: Without a dialer deadline (Murmel dials without one because the
TOFU trust dialog runs inside the TLS handshake) a server that closed the
connection before syncing left Dial — and the connect flow — blocked
forever with no error shown.

**Upstream**: layeh/gumble `gumble/client.go` (same select without an end
case); not reported upstream.

## P11 — Announce protocol version 1.4.0 and release "Murmel"

**What**: `ClientVersion` is 1.4.0 (was 1.3.0); the Version packet's
release string is "Murmel" (was "gumble").

**Why**: murmur ≥ 1.4 sends every pre-1.4 client a "[WARNING]: This
server has the ChannelListener feature enabled but your client version
does not support it" text message on connect. 1.4 is the highest version
that keeps the legacy UDP audio format; 1.5.0 would make murmur switch to
protobuf UDP audio packets, which this fork does not speak.

**Upstream**: layeh/gumble `gumble/client.go` (`ClientVersion = 1<<16 | 3<<8 | 0`).

## P12 — Expose server version, bandwidth limit and server-side crypt stats

**What**: `Client.ServerVersion()`, `Client.MaxBandwidth()` and
`Client.ServerCryptStats()` (the good/late/lost counters the server
reports for our UDP stream in its ping replies). Upstream discarded the
Version message and these Ping fields.

**Why**: Diagnosing voice that leaves the client but never reaches other
users requires knowing what the server received and decrypted.

**Upstream**: layeh/gumble `gumble/handlers.go` (`handleVersion` is a
no-op; `handlePing` reads only the timestamp).

## P13 — Scale the encoder byte budget with the packet length

**What**: `AudioBuffer.writeAudio` multiplies `Config.AudioDataBytes` by
the number of 10 ms frames in the buffer.

**Why**: Murmel sends 20 ms packets by default (like the official
client) and offers 10/20/40/60 ms. The byte budget was per buffer, so a
20 ms buffer at the 40-byte default would cap Opus at half the bitrate.

**Upstream**: layeh/gumble `gumble/audio.go` (`dataBytes := client.Config.AudioDataBytes`).
