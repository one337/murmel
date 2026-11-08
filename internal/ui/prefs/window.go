// Package prefs implements the application preferences window (T25).
// It is an adw.PreferencesWindow with five pages covering identity,
// audio input, audio output, positional audio, and behavior settings.
// All widgets bind directly to the Settings module with instant-apply.
package prefs

import (
	"fmt"
	"log/slog"

	"github.com/diamondburned/gotk4-adwaita/pkg/adw"
	coreglib "github.com/diamondburned/gotk4/pkg/core/glib"
	"github.com/diamondburned/gotk4/pkg/gtk/v4"

	"io.github/murmel/internal/audio/device"
	"io.github/murmel/internal/audio/effects"
	"io.github/murmel/internal/audio/engine"
	"io.github/murmel/internal/audio/jitter"
	"io.github/murmel/internal/bus"
	"io.github/murmel/internal/certs"
	"io.github/murmel/internal/settings"
	"io.github/murmel/internal/ui/levelmeter"
)

// Window is the preferences dialog. It is not reused: create a new one each
// time the user opens Preferences. Present it with w.Present(parent).
type Window struct {
	*adw.PreferencesWindow
}

// New constructs the preferences window. It does not present itself.
func New(st settings.Settings, m *certs.Manager, player *effects.Player, b *bus.Bus) *Window {
	pw := adw.NewPreferencesWindow()
	pw.SetTitle(tr("Preferences"))
	pw.SetSearchEnabled(false)

	pw.Add(identityPage(st, m))
	pw.Add(audioInputPage(st, b))
	pw.Add(audioOutputPage(st))
	pw.Add(behaviorPage(st, player, func() *gtk.Window { return &pw.Window.Window }))

	return &Window{pw}
}

// --- Identity page ---------------------------------------------------------

func identityPage(st settings.Settings, m *certs.Manager) *adw.PreferencesPage {
	page := adw.NewPreferencesPage()
	page.SetTitle(tr("Identity"))
	page.SetName("identity")
	page.SetIconName("system-users-symbolic")

	identities, _ := m.List()
	names := make([]string, 0, len(identities)+1)
	names = append(names, tr("Anonymous"))
	for _, id := range identities {
		names = append(names, id.Name)
	}

	store := gtk.NewStringList(names)
	row := adw.NewComboRow()
	row.SetTitle(tr("Default identity"))
	row.SetModel(store)
	if lastID := st.LastIdentity(); lastID != "" {
		for i, id := range identities {
			if id.ID == lastID {
				row.SetSelected(uint(i + 1))
				break
			}
		}
	}
	coreglib.InternObject(row).NotifyProperty("selected", func() {
		sel := int(row.Selected())
		if sel == 0 {
			st.SetLastIdentity("")
			return
		}
		if sel-1 < len(identities) {
			st.SetLastIdentity(identities[sel-1].ID)
		}
	})

	group := adw.NewPreferencesGroup()
	group.SetTitle(tr("Client Certificate"))
	group.Add(row)

	// Import / export buttons as action rows
	importRow := adw.NewActionRow()
	importRow.SetTitle(tr("Import identity…"))
	importRow.SetSubtitle(tr("Load a PEM certificate + key pair"))
	importRow.SetActivatable(true)
	importRow.AddSuffix(gtk.NewImageFromIconName("document-open-symbolic"))
	importRow.ConnectActivated(func() {
		// TODO T25: open file chooser dialog and call m.Import(pemData)
		slog.Info("prefs: import identity (not yet wired)")
	})

	exportRow := adw.NewActionRow()
	exportRow.SetTitle(tr("Export identity…"))
	exportRow.SetSubtitle(tr("Save the selected identity as PEM"))
	exportRow.SetActivatable(true)
	exportRow.AddSuffix(gtk.NewImageFromIconName("document-save-symbolic"))
	exportRow.ConnectActivated(func() {
		// TODO T25: open file chooser dialog and call m.Export(id)
		slog.Info("prefs: export identity (not yet wired)")
	})

	group.Add(importRow)
	group.Add(exportRow)
	page.Add(group)
	return page
}

// --- Audio Input page ------------------------------------------------------

func audioInputPage(st settings.Settings, b *bus.Bus) *adw.PreferencesPage {
	page := adw.NewPreferencesPage()
	page.SetTitle(tr("Audio Input"))
	page.SetName("audio-input")
	page.SetIconName("audio-input-microphone-symbolic")

	devGroup := adw.NewPreferencesGroup()
	devGroup.SetTitle(tr("Capture Device"))
	devGroup.Add(deviceComboRow(tr("Device"), true, st.AudioInputDevice,
		st.SetAudioInputDevice))

	noise := adw.NewComboRow()
	noise.SetTitle(tr("Noise Suppression"))
	noise.SetSubtitle(tr("Removes steady background noise such as fans, hum or traffic from your voice"))
	noise.SetModel(gtk.NewStringList([]string{tr("Off"), tr("Low"), tr("Moderate"), tr("High"), tr("Very High")}))
	noise.SetSelected(uint(min(4, max(0, st.NoiseSuppression()))))
	coreglib.InternObject(noise).NotifyProperty("selected", func() {
		st.SetNoiseSuppression(int(noise.Selected()))
	})
	devGroup.Add(noise)

	vadGroup := adw.NewPreferencesGroup()
	vadGroup.SetTitle(tr("Voice Detection"))

	vadGroup.SetDescription(tr("Speak normally and drag the handle just below your speech level: everything louder than the handle is transmitted. Without a connection the meter tests the selected microphone."))
	meterBox := gtk.NewBox(gtk.OrientationVertical, 6)
	meterBox.SetMarginTop(12)
	meterBox.SetMarginBottom(12)
	meterBox.SetMarginStart(12)
	meterBox.SetMarginEnd(12)
	title := gtk.NewLabel(tr("Activation Threshold"))
	title.SetXAlign(0)
	meterBox.Append(title)
	meterBox.Append(levelmeter.New(st, b).Root())
	meterRow := gtk.NewListBoxRow()
	meterRow.SetActivatable(false)
	meterRow.SetChild(meterBox)
	vadGroup.Add(meterRow)

	// Audio per packet: the official client's "Audio per packet" setting.
	packetSizes := []int{1, 2, 4, 6} // 10 ms frames
	packetRow := adw.NewComboRow()
	packetRow.SetTitle(tr("Audio per packet"))
	packetRow.SetSubtitle(tr("Longer packets need less bandwidth but add delay. Default: 20 ms"))
	labels := make([]string, len(packetSizes))
	for i, n := range packetSizes {
		labels[i] = fmt.Sprintf(tr("%d ms"), n*10)
	}
	packetRow.SetModel(gtk.NewStringList(labels))
	for i, n := range packetSizes {
		if n == st.AudioPacketFrames() {
			packetRow.SetSelected(uint(i))
		}
	}
	// Quality: the Opus bitrate, like the official client's "Quality".
	quality := adw.NewSpinRow(gtk.NewAdjustment(float64(st.AudioQuality()), 32, 192, 1, 8, 0), 8, 0)
	quality.SetTitle(tr("Quality (kb/s)"))
	updateQualityHint := func() {
		total := engine.NetworkBandwidth(st.AudioQuality()*1000, st.AudioPacketFrames())
		quality.SetSubtitle(fmt.Sprintf(
			tr("Needs about %.1f kb/s including network overhead. Servers with a lower limit reduce it automatically. Default: 40 kb/s"),
			float64(total)/1000))
	}
	updateQualityHint()
	coreglib.InternObject(quality).NotifyProperty("value", func() {
		st.SetAudioQuality(int(quality.Value()))
		updateQualityHint()
	})

	coreglib.InternObject(packetRow).NotifyProperty("selected", func() {
		if sel := int(packetRow.Selected()); sel < len(packetSizes) {
			st.SetAudioPacketFrames(packetSizes[sel])
			updateQualityHint()
		}
	})
	txGroup := adw.NewPreferencesGroup()
	txGroup.SetTitle(tr("Transmission"))
	txGroup.Add(quality)
	txGroup.Add(packetRow)

	page.Add(devGroup)
	page.Add(vadGroup)
	page.Add(txGroup)
	return page
}

// --- Audio Output page -----------------------------------------------------

func audioOutputPage(st settings.Settings) *adw.PreferencesPage {
	page := adw.NewPreferencesPage()
	page.SetTitle(tr("Audio Output"))
	page.SetName("audio-output")
	page.SetIconName("audio-speakers-symbolic")

	devGroup := adw.NewPreferencesGroup()
	devGroup.SetTitle(tr("Playback Device"))
	devGroup.Add(deviceComboRow(tr("Device"), false, st.AudioOutputDevice,
		st.SetAudioOutputDevice))

	jitterGroup := adw.NewPreferencesGroup()
	jitterGroup.SetTitle(tr("Jitter Buffer"))

	// Shown in milliseconds, stored in 10 ms frames.
	jitterSpin := adw.NewSpinRow(gtk.NewAdjustment(
		float64(st.JitterTargetFrames()*10), float64(jitter.MinTargetDepth*10),
		float64(jitter.MaxTargetDepth*10), 10, 10, 0), 10, 0)
	jitterSpin.SetTitle(tr("Buffer (ms)"))
	jitterSpin.SetSubtitle(tr("Raise it if voices stutter on unstable connections; lower values reduce delay. Default: 30 ms"))
	jitterSpin.SetSnapToTicks(true)
	coreglib.InternObject(jitterSpin).NotifyProperty("value", func() {
		st.SetJitterTargetFrames(int(jitterSpin.Value()+5) / 10)
	})
	jitterGroup.Add(jitterSpin)

	page.Add(devGroup)
	page.Add(jitterGroup)
	return page
}

// --- Behavior page ---------------------------------------------------------

func behaviorPage(st settings.Settings, player *effects.Player, parent func() *gtk.Window) *adw.PreferencesPage {
	page := adw.NewPreferencesPage()
	page.SetTitle(tr("Behavior"))
	page.SetName("behavior")
	page.SetIconName("preferences-other-symbolic")

	group := adw.NewPreferencesGroup()
	group.SetTitle(tr("Connection"))

	reconnectRow := adw.NewSwitchRow()
	reconnectRow.SetTitle(tr("Auto-reconnect"))
	reconnectRow.SetSubtitle(tr("Reconnect automatically after a connection drop"))
	reconnectRow.SetActive(st.AutoReconnect())
	coreglib.InternObject(reconnectRow).NotifyProperty("active", func() {
		st.SetAutoReconnect(reconnectRow.Active())
	})
	group.Add(reconnectRow)

	notifGroup := adw.NewPreferencesGroup()
	notifGroup.SetTitle(tr("Notifications"))

	notifRow := adw.NewSwitchRow()
	notifRow.SetTitle(tr("Desktop notifications"))
	notifRow.SetSubtitle(tr("Private messages and reconnects while Murmel is in the background"))
	notifRow.SetActive(st.NotificationsEnabled())
	coreglib.InternObject(notifRow).NotifyProperty("active", func() {
		st.SetNotificationsEnabled(notifRow.Active())
	})
	notifGroup.Add(notifRow)

	presence := func(title, subtitle string, get func() bool, set func(bool)) *adw.SwitchRow {
		row := adw.NewSwitchRow()
		row.SetTitle(title)
		row.SetSubtitle(subtitle)
		row.SetActive(get())
		row.SetSensitive(st.NotificationsEnabled())
		coreglib.InternObject(row).NotifyProperty("active", func() { set(row.Active()) })
		notifGroup.Add(row)
		return row
	}
	channelRow := presence(tr("Users in my channel"),
		tr("When someone joins or leaves your channel"),
		st.NotifyChannelPresence, st.SetNotifyChannelPresence)
	serverRow := presence(tr("Users on the server"),
		tr("When someone connects to or disconnects from the server"),
		st.NotifyServerPresence, st.SetNotifyServerPresence)
	coreglib.InternObject(notifRow).NotifyProperty("active", func() {
		channelRow.SetSensitive(notifRow.Active())
		serverRow.SetSensitive(notifRow.Active())
	})
	notifGroup.Add(soundsRow(st, player, parent))

	page.Add(group)
	page.Add(notifGroup)
	return page
}

// --- Helpers ---------------------------------------------------------------

// deviceComboRow builds a ComboRow backed by audio device enumeration.
// getStored/setStored read and write the device-name key to settings.
func deviceComboRow(title string, capture bool,
	getStored func() string, setStored func(string)) *adw.ComboRow {

	row := adw.NewComboRow()
	row.SetTitle(title)

	var infos []device.Info
	names := []string{tr("System default")}

	if list, err := device.GetDevices(); err == nil {
		if capture {
			infos = list.Capture
		} else {
			infos = list.Playback
		}
		for _, d := range infos {
			names = append(names, d.Name)
		}
	}

	row.SetModel(gtk.NewStringList(names))

	stored := getStored()
	for i, d := range infos {
		if d.Name == stored {
			row.SetSelected(uint(i + 1))
			break
		}
	}

	coreglib.InternObject(row).NotifyProperty("selected", func() {
		sel := int(row.Selected())
		if sel == 0 {
			setStored("")
			return
		}
		if sel-1 < len(infos) {
			setStored(infos[sel-1].Name)
		}
	})

	return row
}
