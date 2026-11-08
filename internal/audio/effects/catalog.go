// Package effects plays Murmel's event sounds (user joins, channel moves,
// messages, …) on the configured output device. It owns the event
// catalogue, the built-in sounds, decoding of custom WAV/Ogg Vorbis files
// and a small mixing player. No GTK imports.
package effects

import (
	"embed"
	"strings"
)

// Event identifies a sound trigger. The string is stored in settings.
type Event string

// Sound events, in the order the preferences list them.
const (
	SelfConnected     Event = "self-connected"
	SelfDisconnected  Event = "self-disconnected"
	SelfMoved         Event = "self-moved"
	UserJoinedChannel Event = "user-joined-channel"
	UserLeftChannel   Event = "user-left-channel"
	UserConnected     Event = "user-connected"
	UserDisconnected  Event = "user-disconnected"
	PrivateMessage    Event = "private-message"
	ChannelMessage    Event = "channel-message"
)

// Events lists every event in display order.
var Events = []Event{
	SelfConnected, SelfDisconnected, SelfMoved,
	UserJoinedChannel, UserLeftChannel,
	UserConnected, UserDisconnected,
	PrivateMessage, ChannelMessage,
}

// Builtins lists the built-in sound IDs in display order.
var Builtins = []string{"join", "leave", "connect", "disconnect", "move", "blip-up", "blip-down", "message", "tick"}

//go:embed sounds/*.ogg
var builtinFS embed.FS

// A choice is what the settings store per event: Off, "builtin:<id>" or
// "file:<path>".
const (
	Off           = "off"
	builtinPrefix = "builtin:"
	filePrefix    = "file:"
)

// BuiltinChoice and FileChoice build choice values.
func BuiltinChoice(id string) string { return builtinPrefix + id }
func FileChoice(path string) string  { return filePrefix + path }

// FilePath returns the path of a "file:" choice.
func FilePath(choice string) (string, bool) {
	return strings.CutPrefix(choice, filePrefix)
}

// BuiltinID returns the ID of a "builtin:" choice.
func BuiltinID(choice string) (string, bool) {
	return strings.CutPrefix(choice, builtinPrefix)
}

// defaults: the user's own connection and channel events and messages to
// them sound by default; server-wide joins/leaves and channel chat are
// opt-in (they get noisy on busy servers).
var defaults = map[Event]string{
	SelfConnected:     BuiltinChoice("connect"),
	SelfDisconnected:  BuiltinChoice("disconnect"),
	SelfMoved:         BuiltinChoice("move"),
	UserJoinedChannel: BuiltinChoice("join"),
	UserLeftChannel:   BuiltinChoice("leave"),
	UserConnected:     Off,
	UserDisconnected:  Off,
	PrivateMessage:    BuiltinChoice("message"),
	ChannelMessage:    Off,
}

// DefaultBuiltin is the built-in sound offered first for an event whose
// default is Off.
var defaultBuiltin = map[Event]string{
	UserConnected:    "blip-up",
	UserDisconnected: "blip-down",
	ChannelMessage:   "tick",
}

// Default returns the default choice for ev.
func Default(ev Event) string {
	if v, ok := defaults[ev]; ok {
		return v
	}
	return Off
}

// SuggestedBuiltin is the built-in sound that fits ev best.
func SuggestedBuiltin(ev Event) string {
	if id, ok := BuiltinID(defaults[ev]); ok {
		return id
	}
	if id, ok := defaultBuiltin[ev]; ok {
		return id
	}
	return Builtins[0]
}

// Choice returns the effective choice for ev given the stored overrides.
func Choice(stored map[string]string, ev Event) string {
	if v, ok := stored[string(ev)]; ok && valid(v) {
		return v
	}
	return Default(ev)
}

func valid(v string) bool {
	if v == Off {
		return true
	}
	if id, ok := BuiltinID(v); ok {
		for _, b := range Builtins {
			if b == id {
				return true
			}
		}
		return false
	}
	path, ok := FilePath(v)
	return ok && path != ""
}
