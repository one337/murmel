package settings

// keySpec binds one schema key to its typed accessors. The specs below are
// the machine-checked counterpart of the mapping table in the package doc:
// every schema key must appear here exactly once (enforced by
// TestKeyCoverage), and the memory/gsettings tests drive every spec through
// the default → set → get → reset cycle.
type keySpec struct {
	key    string // schema key name
	gtype  string // GVariant type as written in the schema ("s", "as", ...)
	def    any    // schema default
	sample any    // in-range, non-default value for roundtrips
	get    func(s Settings) any
	set    func(s Settings, v any)
}

func sampleServer() ServerEntry {
	return ServerEntry{
		Name:       "Community",
		Host:       "mumble.example.org",
		Port:       64738,
		Username:   "alice",
		IdentityID: "sha256:abcd1234",
	}
}

var keySpecs = map[string]keySpec{
	"first-run": {
		key: "first-run", gtype: "b", def: true, sample: false,
		get: func(s Settings) any { return s.FirstRun() },
		set: func(s Settings, v any) { s.SetFirstRun(v.(bool)) },
	},
	"last-identity": {
		key: "last-identity", gtype: "s", def: "", sample: "sha256:abcd1234",
		get: func(s Settings) any { return s.LastIdentity() },
		set: func(s Settings, v any) { s.SetLastIdentity(v.(string)) },
	},
	"server-list": {
		key: "server-list", gtype: "as", def: []ServerEntry{}, sample: []ServerEntry{sampleServer()},
		get: func(s Settings) any { return s.Servers() },
		set: func(s Settings, v any) { s.SetServers(v.([]ServerEntry)) },
	},
	"audio-input-device": {
		key: "audio-input-device", gtype: "s", def: "", sample: "alsa_input.usb-Yeti",
		get: func(s Settings) any { return s.AudioInputDevice() },
		set: func(s Settings, v any) { s.SetAudioInputDevice(v.(string)) },
	},
	"audio-output-device": {
		key: "audio-output-device", gtype: "s", def: "", sample: "alsa_output.pci-0000_00_1f.3",
		get: func(s Settings) any { return s.AudioOutputDevice() },
		set: func(s Settings, v any) { s.SetAudioOutputDevice(v.(string)) },
	},
	"vad-threshold": {
		key: "vad-threshold", gtype: "d", def: 0.35, sample: 0.7,
		get: func(s Settings) any { return s.VADThreshold() },
		set: func(s Settings, v any) { s.SetVADThreshold(v.(float64)) },
	},
	"jitter-target-frames": {
		key: "jitter-target-frames", gtype: "i", def: 3, sample: 6,
		get: func(s Settings) any { return s.JitterTargetFrames() },
		set: func(s Settings, v any) { s.SetJitterTargetFrames(v.(int)) },
	},
	"audio-packet-frames": {
		key: "audio-packet-frames", gtype: "i", def: 2, sample: 4,
		get: func(s Settings) any { return s.AudioPacketFrames() },
		set: func(s Settings, v any) { s.SetAudioPacketFrames(v.(int)) },
	},
	"noise-suppression": {
		key: "noise-suppression", gtype: "i", def: 2, sample: 4,
		get: func(s Settings) any { return s.NoiseSuppression() },
		set: func(s Settings, v any) { s.SetNoiseSuppression(v.(int)) },
	},
	"echo-cancellation": {
		key: "echo-cancellation", gtype: "b", def: false, sample: true,
		get: func(s Settings) any { return s.EchoCancellation() },
		set: func(s Settings, v any) { s.SetEchoCancellation(v.(bool)) },
	},
	"audio-quality": {
		key: "audio-quality", gtype: "i", def: 40, sample: 96,
		get: func(s Settings) any { return s.AudioQuality() },
		set: func(s Settings, v any) { s.SetAudioQuality(v.(int)) },
	},
	"notification-enabled": {
		key: "notification-enabled", gtype: "b", def: true, sample: false,
		get: func(s Settings) any { return s.NotificationsEnabled() },
		set: func(s Settings, v any) { s.SetNotificationsEnabled(v.(bool)) },
	},
	"notify-channel-presence": {
		key: "notify-channel-presence", gtype: "b", def: false, sample: true,
		get: func(s Settings) any { return s.NotifyChannelPresence() },
		set: func(s Settings, v any) { s.SetNotifyChannelPresence(v.(bool)) },
	},
	"notify-server-presence": {
		key: "notify-server-presence", gtype: "b", def: false, sample: true,
		get: func(s Settings) any { return s.NotifyServerPresence() },
		set: func(s Settings, v any) { s.SetNotifyServerPresence(v.(bool)) },
	},
	"chat-log": {
		key: "chat-log", gtype: "a{sb}", def: map[string]bool{},
		sample: map[string]bool{"channel-join": false, "server-connect": true},
		get:    func(s Settings) any { return s.ChatLogEvents() },
		set:    func(s Settings, v any) { s.SetChatLogEvents(v.(map[string]bool)) },
	},
	"sounds-enabled": {
		key: "sounds-enabled", gtype: "b", def: true, sample: false,
		get: func(s Settings) any { return s.SoundsEnabled() },
		set: func(s Settings, v any) { s.SetSoundsEnabled(v.(bool)) },
	},
	"sound-events": {
		key: "sound-events", gtype: "a{ss}", def: map[string]string{},
		sample: map[string]string{"user-joined-channel": "off", "private-message": "file:/tmp/ping.ogg"},
		get:    func(s Settings) any { return s.SoundEvents() },
		set:    func(s Settings, v any) { s.SetSoundEvents(v.(map[string]string)) },
	},
	"auto-reconnect": {
		key: "auto-reconnect", gtype: "b", def: true, sample: false,
		get: func(s Settings) any { return s.AutoReconnect() },
		set: func(s Settings, v any) { s.SetAutoReconnect(v.(bool)) },
	},
	"window-width": {
		key: "window-width", gtype: "i", def: 900, sample: 1280,
		get: func(s Settings) any { return s.WindowWidth() },
		set: func(s Settings, v any) { s.SetWindowWidth(v.(int)) },
	},
	"window-height": {
		key: "window-height", gtype: "i", def: 700, sample: 1024,
		get: func(s Settings) any { return s.WindowHeight() },
		set: func(s Settings, v any) { s.SetWindowHeight(v.(int)) },
	},
	"window-maximized": {
		key: "window-maximized", gtype: "b", def: false, sample: true,
		get: func(s Settings) any { return s.WindowMaximized() },
		set: func(s Settings, v any) { s.SetWindowMaximized(v.(bool)) },
	},
}
