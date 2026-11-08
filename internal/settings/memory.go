package settings

import (
	"maps"
	"sync"
)

// memoryDefaults mirrors the <default> of every key in
// data/io.github.murmel.Murmel.gschema.xml. coverage_test.go fails when
// this map drifts from the schema file.
func memoryDefaults() map[string]any {
	return map[string]any{
		"first-run":               true,
		"last-identity":           "",
		"server-list":             []ServerEntry{},
		"audio-input-device":      "",
		"audio-output-device":     "",
		"vad-threshold":           0.5,
		"jitter-target-frames":    3,
		"audio-packet-frames":     2,
		"audio-quality":           40,
		"notification-enabled":    true,
		"notify-channel-presence": false,
		"notify-server-presence":  false,
		"sounds-enabled":          true,
		"sound-events":            map[string]string{},
		"auto-reconnect":          true,
		"window-width":            900,
		"window-height":           700,
		"window-maximized":        false,
	}
}

// Memory is the pure-Go Settings implementation used for tests and for dev
// sessions where the schema is not installed. It is seeded with the schema
// defaults and keeps all state in-process. The mutex makes it safe for
// plain `go test` usage; the GSettings contract itself stays main-thread
// (see package doc).
type Memory struct {
	mu       sync.Mutex
	values   map[string]any
	defaults map[string]any
	handlers map[int]func(key string)
	nextID   int
}

// NewMemory returns an empty Memory store seeded with the schema defaults.
func NewMemory() *Memory {
	return &Memory{
		values:   map[string]any{},
		defaults: memoryDefaults(),
		handlers: map[int]func(key string){},
	}
}

func (m *Memory) get(key string) any {
	m.mu.Lock()
	defer m.mu.Unlock()
	if v, ok := m.values[key]; ok {
		return v
	}
	return m.defaults[key]
}

func (m *Memory) set(key string, v any) {
	m.mu.Lock()
	m.values[key] = v
	handlers := make([]func(string), 0, len(m.handlers))
	for _, h := range m.handlers {
		handlers = append(handlers, h)
	}
	m.mu.Unlock()
	for _, h := range handlers {
		h(key)
	}
}

func (m *Memory) FirstRun() bool           { return m.get("first-run").(bool) }
func (m *Memory) SetFirstRun(v bool)       { m.set("first-run", v) }
func (m *Memory) LastIdentity() string     { return m.get("last-identity").(string) }
func (m *Memory) SetLastIdentity(v string) { m.set("last-identity", v) }
func (m *Memory) AudioInputDevice() string { return m.get("audio-input-device").(string) }
func (m *Memory) SetAudioInputDevice(v string) {
	m.set("audio-input-device", v)
}
func (m *Memory) AudioOutputDevice() string { return m.get("audio-output-device").(string) }
func (m *Memory) SetAudioOutputDevice(v string) {
	m.set("audio-output-device", v)
}
func (m *Memory) VADThreshold() float64     { return m.get("vad-threshold").(float64) }
func (m *Memory) SetVADThreshold(v float64) { m.set("vad-threshold", v) }
func (m *Memory) JitterTargetFrames() int   { return m.get("jitter-target-frames").(int) }
func (m *Memory) SetJitterTargetFrames(v int) {
	m.set("jitter-target-frames", v)
}
func (m *Memory) AudioPacketFrames() int { return m.get("audio-packet-frames").(int) }
func (m *Memory) SetAudioPacketFrames(v int) {
	m.set("audio-packet-frames", v)
}
func (m *Memory) NotificationsEnabled() bool { return m.get("notification-enabled").(bool) }
func (m *Memory) SetNotificationsEnabled(v bool) {
	m.set("notification-enabled", v)
}
func (m *Memory) AutoReconnect() bool       { return m.get("auto-reconnect").(bool) }
func (m *Memory) SetAutoReconnect(v bool)   { m.set("auto-reconnect", v) }
func (m *Memory) WindowWidth() int          { return m.get("window-width").(int) }
func (m *Memory) SetWindowWidth(v int)      { m.set("window-width", v) }
func (m *Memory) WindowHeight() int         { return m.get("window-height").(int) }
func (m *Memory) SetWindowHeight(v int)     { m.set("window-height", v) }
func (m *Memory) WindowMaximized() bool     { return m.get("window-maximized").(bool) }
func (m *Memory) SetWindowMaximized(v bool) { m.set("window-maximized", v) }

func (m *Memory) Servers() []ServerEntry {
	v := m.get("server-list")
	if v == nil {
		return nil
	}
	return append([]ServerEntry{}, v.([]ServerEntry)...)
}

func (m *Memory) SetServers(v []ServerEntry) {
	if v == nil {
		v = []ServerEntry{}
	}
	m.set("server-list", append([]ServerEntry{}, v...))
}

func (m *Memory) Reset(key string) {
	m.mu.Lock()
	delete(m.values, key)
	handlers := make([]func(string), 0, len(m.handlers))
	for _, h := range m.handlers {
		handlers = append(handlers, h)
	}
	m.mu.Unlock()
	for _, h := range handlers {
		h(key)
	}
}

func (m *Memory) ConnectChanged(f func(key string)) func() {
	m.mu.Lock()
	id := m.nextID
	m.nextID++
	m.handlers[id] = f
	m.mu.Unlock()
	return func() {
		m.mu.Lock()
		delete(m.handlers, id)
		m.mu.Unlock()
	}
}

func (m *Memory) NotifyChannelPresence() bool { return m.get("notify-channel-presence").(bool) }
func (m *Memory) SetNotifyChannelPresence(v bool) {
	m.set("notify-channel-presence", v)
}
func (m *Memory) NotifyServerPresence() bool { return m.get("notify-server-presence").(bool) }
func (m *Memory) SetNotifyServerPresence(v bool) {
	m.set("notify-server-presence", v)
}

func (m *Memory) SoundsEnabled() bool     { return m.get("sounds-enabled").(bool) }
func (m *Memory) SetSoundsEnabled(v bool) { m.set("sounds-enabled", v) }

func (m *Memory) SoundEvents() map[string]string {
	return maps.Clone(m.get("sound-events").(map[string]string))
}

func (m *Memory) SetSoundEvents(v map[string]string) {
	c := maps.Clone(v)
	if c == nil {
		c = map[string]string{}
	}
	m.set("sound-events", c)
}

func (m *Memory) AudioQuality() int     { return m.get("audio-quality").(int) }
func (m *Memory) SetAudioQuality(v int) { m.set("audio-quality", v) }
