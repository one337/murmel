// Package settings manages application settings persistence via
// GSettings/GSchema.
//
// This package is the typed accessor layer over the application's GSchema
// keys (data/io.github.murmel.Murmel.gschema.xml). It is the ONLY
// internal package allowed to import gio/gtk — this exemption is by design
// and documented in internal/layer_test.go, whose scope covers only
// internal/protocol and internal/audio.
//
// # Implementations
//
// Two implementations of the [Settings] interface exist:
//
//   - [GSettings]: the real implementation backed by GSettings (dconf inside
//     the Flatpak, the GSettings "memory" backend in tests). Created via
//     [NewGSettings]; fails if the schema cannot be resolved.
//   - [Memory]: a pure-Go implementation seeded with the schema defaults.
//     It enables unit tests (and bare `go run` sessions) without installing
//     schemas anywhere.
//
// [New] picks GSettings when the schema is resolvable and silently falls
// back to [Memory] otherwise (development convenience; the Flatpak always
// ships the compiled schema).
//
// There is deliberately NO JSON config file fallback.
//
// # Threading
//
// The settings module runs on the GTK main thread. The GSettings
// implementation must only be used from the main loop thread (GSettings is
// not documented thread-safe for concurrent mutation, and changed-signal
// handlers fire on the thread-default main context). The Memory
// implementation additionally guards its map with a mutex so plain `go
// test` can use it freely, but callers should still treat Settings as
// main-thread-owned.
//
// # Key ↔ accessor ↔ preference widget mapping
//
// This table is the single source of truth that task T25 (preferences
// dialog) maps against; coverage_test.go enforces that it stays in sync
// with the schema XML:
//
//	Schema key           Typed accessors                            T25 widget
//	first-run            FirstRun / SetFirstRun                     (none: first-run flow)
//	last-identity        LastIdentity / SetLastIdentity             identity picker
//	server-list          Servers / SetServers                       favorites list
//	audio-input-device   AudioInputDevice / SetAudioInputDevice     input device combo
//	audio-output-device  AudioOutputDevice / SetAudioOutputDevice   output device combo
//	vad-threshold        VADThreshold / SetVADThreshold             sensitivity scale
//	jitter-target-frames JitterTargetFrames / SetJitterTargetFrames jitter buffer spin (20–80 ms)
//	audio-packet-frames  AudioPacketFrames / SetAudioPacketFrames   audio per packet combo (10–60 ms)
//	notification-enabled NotificationsEnabled / SetNotifications   notifications switch
//	notify-channel-presence NotifyChannelPresence / SetNotifyChannelPresence channel join/leave switch
//	notify-server-presence  NotifyServerPresence / SetNotifyServerPresence   server join/leave switch
//	sounds-enabled       SoundsEnabled / SetSoundsEnabled           sounds expander switch
//	sound-events         SoundEvents / SetSoundEvents               per-event sound combos
//	auto-reconnect       AutoReconnect / SetAutoReconnect           reconnect switch
//	window-width         WindowWidth / SetWindowWidth              (none: saved on close)
//	window-height        WindowHeight / SetWindowHeight            (none: saved on close)
//	window-maximized     WindowMaximized / SetWindowMaximized      (none: saved on close)
//
// server-list entries are [ServerEntry] values serialized as compact JSON
// strings inside the GSettings string array (format documented in the
// schema file itself).
package settings
