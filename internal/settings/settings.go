package settings

// SchemaID is the GSettings schema id of the application. It must match the
// id in data/io.github.murmel.Murmel.gschema.xml.
const SchemaID = "io.github.murmel.Murmel"

// ServerEntry is one favorite server stored in the server-list key. Each
// entry is serialized as a compact JSON object string (fields "name",
// "host", "port", "username", "identity-id") inside the GSettings string
// array.
type ServerEntry struct {
	Name       string `json:"name"`
	Host       string `json:"host"`
	Port       int    `json:"port"`
	Username   string `json:"username"`
	IdentityID string `json:"identity-id"`
}

// Settings is the typed accessor layer over the GSchema keys. See the
// package doc comment for the key ↔ accessor ↔ T25 widget mapping table.
//
// All methods must be called from the GTK main thread (see package doc).
// Setters that violate a schema range are refused by the GSettings backend
// (the previous value is kept); UI layers are expected to constrain values
// beforehand.
type Settings interface {
	FirstRun() bool
	SetFirstRun(v bool)

	LastIdentity() string
	SetLastIdentity(v string)

	Servers() []ServerEntry
	SetServers(v []ServerEntry)

	AudioInputDevice() string
	SetAudioInputDevice(v string)

	AudioOutputDevice() string
	SetAudioOutputDevice(v string)

	VADThreshold() float64
	SetVADThreshold(v float64)

	JitterTargetFrames() int
	SetJitterTargetFrames(v int)

	AudioPacketFrames() int
	SetAudioPacketFrames(v int)

	// NoiseSuppression is the microphone noise suppression strength:
	// 0 off, 1 low, 2 moderate, 3 high, 4 very high.
	NoiseSuppression() int
	SetNoiseSuppression(v int)

	EchoCancellation() bool
	SetEchoCancellation(v bool)

	// Language is the interface language code ("" = the system's).
	Language() string
	SetLanguage(v string)

	// ColorScheme is "system", "light" or "dark".
	ColorScheme() string
	SetColorScheme(v string)

	// AudioQuality is the outgoing Opus bitrate in kbit/s.
	AudioQuality() int
	SetAudioQuality(v int)

	NotificationsEnabled() bool
	SetNotificationsEnabled(v bool)

	NotifyChannelPresence() bool
	SetNotifyChannelPresence(v bool)

	NotifyServerPresence() bool
	SetNotifyServerPresence(v bool)

	// ChatLogEvents returns the per-event overrides of the channel chat
	// log (event → shown); the map is a copy.
	ChatLogEvents() map[string]bool
	SetChatLogEvents(v map[string]bool)

	SoundsEnabled() bool
	SetSoundsEnabled(v bool)

	// SoundEvents returns the per-event sound overrides (event → choice,
	// see internal/audio/effects); the map is a copy.
	SoundEvents() map[string]string
	SetSoundEvents(v map[string]string)

	AutoReconnect() bool
	SetAutoReconnect(v bool)

	WindowWidth() int
	SetWindowWidth(v int)

	WindowHeight() int
	SetWindowHeight(v int)

	WindowMaximized() bool
	SetWindowMaximized(v bool)

	// Reset restores the schema default of key. key must be a schema key
	// name (see the package doc mapping table).
	Reset(key string)

	// ConnectChanged registers f to be invoked (on the main thread) with
	// the schema key name whenever a value changes. It returns a function
	// disconnecting f, intended for widget teardown.
	ConnectChanged(f func(key string)) (disconnect func())
}

// New returns the process settings store: the GSettings-backed
// implementation when SchemaID is resolvable (installed system-wide or via
// GSETTINGS_SCHEMA_DIR), otherwise the Memory implementation. The fallback
// keeps plain `go run`/`go test` sessions working without installing
// schemas; the Flatpak always ships the compiled schema.
func New() Settings {
	if gs, err := NewGSettings(); err == nil {
		return gs
	}
	return NewMemory()
}
