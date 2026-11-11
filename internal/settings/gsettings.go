package settings

import (
	"bytes"
	"encoding/json"
	"fmt"
	"maps"
	"slices"

	"github.com/diamondburned/gotk4/pkg/gio/v2"
	"github.com/diamondburned/gotk4/pkg/glib/v2"
)

// GSettings is the Settings implementation backed by real GSettings (dconf
// inside the Flatpak; the GSettings "memory" backend under tests). It must
// only be used from the GTK main thread (see package doc).
type GSettings struct {
	gs *gio.Settings
}

// SchemaAvailable reports whether SchemaID can be resolved from the default
// schema source (installed schemas plus GSETTINGS_SCHEMA_DIR).
func SchemaAvailable() bool {
	src := gio.SettingsSchemaSourceGetDefault()
	return src != nil && src.Lookup(SchemaID, true) != nil
}

// NewGSettings connects to the real GSettings database for SchemaID. It
// returns an error when the schema is not resolvable; callers that can live
// without persistence should fall back to NewMemory (see New).
func NewGSettings() (*GSettings, error) {
	if !SchemaAvailable() {
		return nil, fmt.Errorf("settings: schema %s not found (install it or set GSETTINGS_SCHEMA_DIR)", SchemaID)
	}
	return &GSettings{gs: gio.NewSettings(SchemaID)}, nil
}

// marshalEntry serializes one ServerEntry as a compact JSON string
// (schema-documented format). Round-trips through unmarshalEntry.
func marshalEntry(e ServerEntry) (string, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(e); err != nil {
		return "", err
	}
	return string(bytes.TrimSpace(buf.Bytes())), nil
}

// unmarshalEntry parses one server-list array element; malformed entries
// yield an error and are skipped by Servers.
func unmarshalEntry(s string) (ServerEntry, error) {
	var e ServerEntry
	if err := json.Unmarshal([]byte(s), &e); err != nil {
		return ServerEntry{}, err
	}
	return e, nil
}

func (g *GSettings) FirstRun() bool           { return g.gs.Boolean("first-run") }
func (g *GSettings) SetFirstRun(v bool)       { g.gs.SetBoolean("first-run", v) }
func (g *GSettings) LastIdentity() string     { return g.gs.String("last-identity") }
func (g *GSettings) SetLastIdentity(v string) { g.gs.SetString("last-identity", v) }
func (g *GSettings) AudioInputDevice() string { return g.gs.String("audio-input-device") }
func (g *GSettings) SetAudioInputDevice(v string) {
	g.gs.SetString("audio-input-device", v)
}
func (g *GSettings) AudioOutputDevice() string { return g.gs.String("audio-output-device") }
func (g *GSettings) SetAudioOutputDevice(v string) {
	g.gs.SetString("audio-output-device", v)
}
func (g *GSettings) VADThreshold() float64 { return g.gs.Double("vad-threshold") }
func (g *GSettings) SetVADThreshold(v float64) {
	g.gs.SetDouble("vad-threshold", v)
}
func (g *GSettings) JitterTargetFrames() int { return g.gs.Int("jitter-target-frames") }
func (g *GSettings) SetJitterTargetFrames(v int) {
	g.gs.SetInt("jitter-target-frames", v)
}
func (g *GSettings) AudioPacketFrames() int { return g.gs.Int("audio-packet-frames") }
func (g *GSettings) SetAudioPacketFrames(v int) {
	g.gs.SetInt("audio-packet-frames", v)
}
func (g *GSettings) NotificationsEnabled() bool { return g.gs.Boolean("notification-enabled") }
func (g *GSettings) SetNotificationsEnabled(v bool) {
	g.gs.SetBoolean("notification-enabled", v)
}
func (g *GSettings) AutoReconnect() bool     { return g.gs.Boolean("auto-reconnect") }
func (g *GSettings) SetAutoReconnect(v bool) { g.gs.SetBoolean("auto-reconnect", v) }
func (g *GSettings) WindowWidth() int        { return g.gs.Int("window-width") }
func (g *GSettings) SetWindowWidth(v int)    { g.gs.SetInt("window-width", v) }
func (g *GSettings) WindowHeight() int       { return g.gs.Int("window-height") }
func (g *GSettings) SetWindowHeight(v int)   { g.gs.SetInt("window-height", v) }
func (g *GSettings) WindowMaximized() bool   { return g.gs.Boolean("window-maximized") }
func (g *GSettings) SetWindowMaximized(v bool) {
	g.gs.SetBoolean("window-maximized", v)
}

func (g *GSettings) Servers() []ServerEntry {
	raw := g.gs.Strv("server-list")
	entries := make([]ServerEntry, 0, len(raw))
	for _, s := range raw {
		e, err := unmarshalEntry(s)
		if err != nil {
			continue
		}
		entries = append(entries, e)
	}
	return entries
}

func (g *GSettings) SetServers(v []ServerEntry) {
	raw := make([]string, 0, len(v))
	for _, e := range v {
		s, err := marshalEntry(e)
		if err != nil {
			continue
		}
		raw = append(raw, s)
	}
	g.gs.SetStrv("server-list", raw)
}

func (g *GSettings) Reset(key string) { g.gs.Reset(key) }

func (g *GSettings) ConnectChanged(f func(key string)) func() {
	handle := g.gs.ConnectChanged(f)
	return func() { g.gs.HandlerDisconnect(handle) }
}

func (g *GSettings) NotifyChannelPresence() bool { return g.gs.Boolean("notify-channel-presence") }
func (g *GSettings) SetNotifyChannelPresence(v bool) {
	g.gs.SetBoolean("notify-channel-presence", v)
}
func (g *GSettings) NotifyServerPresence() bool { return g.gs.Boolean("notify-server-presence") }
func (g *GSettings) SetNotifyServerPresence(v bool) {
	g.gs.SetBoolean("notify-server-presence", v)
}

func (g *GSettings) SoundsEnabled() bool     { return g.gs.Boolean("sounds-enabled") }
func (g *GSettings) SetSoundsEnabled(v bool) { g.gs.SetBoolean("sounds-enabled", v) }

func (g *GSettings) SoundEvents() map[string]string {
	v := g.gs.Value("sound-events")
	out := make(map[string]string, v.NChildren())
	for i := uint(0); i < v.NChildren(); i++ {
		entry := v.ChildValue(i)
		out[entry.ChildValue(0).String()] = entry.ChildValue(1).String()
	}
	return out
}

func (g *GSettings) SetSoundEvents(v map[string]string) {
	keys := slices.Sorted(maps.Keys(v))
	entries := make([]*glib.Variant, 0, len(keys))
	for _, k := range keys {
		entries = append(entries, glib.NewVariantDictEntry(glib.NewVariantString(k), glib.NewVariantString(v[k])))
	}
	g.gs.SetValue("sound-events", glib.NewVariantArray(glib.NewVariantType("{ss}"), entries))
}

func (g *GSettings) AudioQuality() int     { return g.gs.Int("audio-quality") }
func (g *GSettings) SetAudioQuality(v int) { g.gs.SetInt("audio-quality", v) }

func (g *GSettings) NoiseSuppression() int     { return g.gs.Int("noise-suppression") }
func (g *GSettings) SetNoiseSuppression(v int) { g.gs.SetInt("noise-suppression", v) }

func (g *GSettings) EchoCancellation() bool       { return g.gs.Boolean("echo-cancellation") }
func (g *GSettings) SetEchoCancellation(v bool)   { g.gs.SetBoolean("echo-cancellation", v) }
func (g *GSettings) Language() string             { return g.gs.String("language") }
func (g *GSettings) SetLanguage(v string)         { g.gs.SetString("language", v) }
func (g *GSettings) TransmissionMode() string     { return g.gs.String("transmission-mode") }
func (g *GSettings) SetTransmissionMode(v string) { g.gs.SetString("transmission-mode", v) }
func (g *GSettings) GlobalShortcuts() bool        { return g.gs.Boolean("global-shortcuts") }
func (g *GSettings) SetGlobalShortcuts(v bool)    { g.gs.SetBoolean("global-shortcuts", v) }
func (g *GSettings) ColorScheme() string          { return g.gs.String("color-scheme") }
func (g *GSettings) SetColorScheme(v string)      { g.gs.SetString("color-scheme", v) }

func (g *GSettings) ChatLogEvents() map[string]bool {
	v := g.gs.Value("chat-log")
	out := make(map[string]bool, v.NChildren())
	for i := uint(0); i < v.NChildren(); i++ {
		entry := v.ChildValue(i)
		out[entry.ChildValue(0).String()] = entry.ChildValue(1).Boolean()
	}
	return out
}

func (g *GSettings) UserVolumes() map[string]float64 {
	v := g.gs.Value("user-volumes")
	out := make(map[string]float64, v.NChildren())
	for i := uint(0); i < v.NChildren(); i++ {
		entry := v.ChildValue(i)
		out[entry.ChildValue(0).String()] = entry.ChildValue(1).Double()
	}
	return out
}

func (g *GSettings) SetUserVolumes(v map[string]float64) {
	keys := slices.Sorted(maps.Keys(v))
	entries := make([]*glib.Variant, 0, len(keys))
	for _, k := range keys {
		entries = append(entries, glib.NewVariantDictEntry(glib.NewVariantString(k), glib.NewVariantDouble(v[k])))
	}
	g.gs.SetValue("user-volumes", glib.NewVariantArray(glib.NewVariantType("{sd}"), entries))
}

func (g *GSettings) UserLocalMutes() map[string]bool { return g.boolMap("user-local-mutes") }
func (g *GSettings) SetUserLocalMutes(v map[string]bool) {
	g.setBoolMap("user-local-mutes", v)
}

func (g *GSettings) boolMap(key string) map[string]bool {
	v := g.gs.Value(key)
	out := make(map[string]bool, v.NChildren())
	for i := uint(0); i < v.NChildren(); i++ {
		entry := v.ChildValue(i)
		out[entry.ChildValue(0).String()] = entry.ChildValue(1).Boolean()
	}
	return out
}

func (g *GSettings) setBoolMap(key string, v map[string]bool) {
	keys := slices.Sorted(maps.Keys(v))
	entries := make([]*glib.Variant, 0, len(keys))
	for _, k := range keys {
		entries = append(entries, glib.NewVariantDictEntry(glib.NewVariantString(k), glib.NewVariantBoolean(v[k])))
	}
	g.gs.SetValue(key, glib.NewVariantArray(glib.NewVariantType("{sb}"), entries))
}

func (g *GSettings) SetChatLogEvents(v map[string]bool) {
	keys := slices.Sorted(maps.Keys(v))
	entries := make([]*glib.Variant, 0, len(keys))
	for _, k := range keys {
		entries = append(entries, glib.NewVariantDictEntry(glib.NewVariantString(k), glib.NewVariantBoolean(v[k])))
	}
	g.gs.SetValue("chat-log", glib.NewVariantArray(glib.NewVariantType("{sb}"), entries))
}
