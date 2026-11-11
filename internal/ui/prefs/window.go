// Package prefs implements the application preferences window.
// It is an adw.PreferencesWindow with five pages covering identity,
// audio input, audio output, positional audio, and behavior settings.
// All widgets bind directly to the Settings module with instant-apply.
package prefs

import (
	"fmt"
	"log/slog"
	"path/filepath"
	"sort"
	"strings"

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
	"io.github/murmel/internal/ui"
	"io.github/murmel/internal/ui/avatarpick"
	"io.github/murmel/internal/ui/chat"
	"io.github/murmel/internal/ui/identities"
	"io.github/murmel/internal/ui/levelmeter"
	"io.github/murmel/internal/ui/shortcuts"
)

// Window is the preferences dialog. It is not reused: create a new one each
// time the user opens Preferences. Present it with w.Present(parent).
type Window struct {
	*adw.PreferencesWindow
}

// Shortcuts is the global shortcuts as the preferences show them (the
// main window owns them); nil hides the group.
type Shortcuts interface {
	Available() (available, known bool)
	Shortcuts() []shortcuts.Shortcut
	Bindings() []shortcuts.Binding
	IsSetUp() bool
	Busy() bool
	SetUp()
	OnChange(f func()) (remove func())
}

// New constructs the preferences window. It does not present itself.
func New(st settings.Settings, m *certs.Manager, player *effects.Player, b *bus.Bus, sc Shortcuts) *Window {
	pw := adw.NewPreferencesWindow()
	pw.SetTitle(tr("Preferences"))
	pw.SetSearchEnabled(false)

	pw.Add(identityPage(st, m, pw))
	pw.Add(audioInputPage(st, b, sc, pw))
	pw.Add(audioOutputPage(st))
	pw.Add(chatPage(st))
	pw.Add(generalPage(st, player, pw, sc))

	return &Window{pw}
}

// --- Identity page ---------------------------------------------------------

func identityPage(st settings.Settings, m *certs.Manager, pw *adw.PreferencesWindow) *adw.PreferencesPage {
	page := adw.NewPreferencesPage()
	page.SetTitle(tr("Identity"))
	page.SetName("identity")
	page.SetIconName("system-users-symbolic")

	// Default avatar: sent on every connection that has no own avatar.
	avatars := avatarpick.NewStore(filepath.Join(m.DataDir(), "avatars"))
	avatarRow := avatarpick.NewRow(tr("Default avatar"), "", tr("Optional — sent to servers for connections without their own avatar"),
		func(data []byte) {
			if err := avatars.Save(avatarpick.DefaultKey, data); err != nil {
				slog.Info("prefs: default avatar not saved", "err", err)
			}
		})
	avatarRow.SetData(avatars.Load(avatarpick.DefaultKey))
	avatarGroup := adw.NewPreferencesGroup()
	avatarGroup.SetTitle(tr("Avatar"))
	avatarGroup.Add(avatarRow)
	page.Add(avatarGroup)

	c := &certList{st: st, m: m, pw: pw}
	page.Add(c.defaultGroup())
	page.Add(c.listGroup())
	c.refresh()
	return page
}

// certList is the Identity page's certificate section: the default
// identity and the list of all identities with export and delete.
type certList struct {
	st settings.Settings
	m  *certs.Manager
	pw *adw.PreferencesWindow

	identities []certs.IdentityMeta
	combo      *adw.ComboRow
	group      *adw.PreferencesGroup
	rows       []gtk.Widgetter
	syncing    bool
}

func (c *certList) defaultGroup() *adw.PreferencesGroup {
	c.combo = adw.NewComboRow()
	c.combo.SetTitle(tr("Default identity"))
	c.combo.SetSubtitle(tr("Used for new connections"))
	coreglib.InternObject(c.combo).NotifyProperty("selected", func() {
		if c.syncing {
			return
		}
		sel := int(c.combo.Selected())
		if sel == 0 || sel-1 >= len(c.identities) {
			c.st.SetLastIdentity("")
			return
		}
		c.st.SetLastIdentity(c.identities[sel-1].ID)
	})
	g := adw.NewPreferencesGroup()
	g.SetTitle(tr("Client Certificate"))
	g.Add(c.combo)
	return g
}

func (c *certList) listGroup() *adw.PreferencesGroup {
	c.group = adw.NewPreferencesGroup()
	c.group.SetTitle(tr("Certificates"))
	c.group.SetDescription(tr("Servers recognize registered accounts by their certificate. Keep a backup of the ones you use."))

	create := gtk.NewButtonWithLabel(tr("Create…"))
	create.SetTooltipText(tr("Create a new certificate"))
	create.ConnectClicked(func() {
		identities.ShowCreate(c.pw, c.m, "", func(meta certs.IdentityMeta) {
			c.st.SetLastIdentity(meta.ID)
			c.refresh()
		})
	})
	imp := gtk.NewButtonWithLabel(tr("Import…"))
	imp.SetTooltipText(tr("Load a PEM file with certificate and private key"))
	imp.ConnectClicked(func() {
		identities.Import(&c.pw.Window.Window, c.m, func(meta certs.IdentityMeta) {
			c.st.SetLastIdentity(meta.ID)
			c.refresh()
			c.pw.AddToast(adw.NewToast(fmt.Sprintf(tr("Imported identity %s"), meta.Name)))
		}, func(err error) {
			c.pw.AddToast(adw.NewToast(fmt.Sprintf(tr("Import failed: %v"), err)))
		})
	})
	buttons := gtk.NewBox(gtk.OrientationHorizontal, 6)
	buttons.SetVAlign(gtk.AlignCenter)
	buttons.Append(create)
	buttons.Append(imp)
	c.group.SetHeaderSuffix(buttons)
	return c.group
}

// refresh re-reads the identity store into the combo and the list.
func (c *certList) refresh() {
	list, err := c.m.List()
	if err != nil {
		slog.Info("prefs: identities not listed", "err", err)
	}
	sort.Slice(list, func(i, j int) bool { return list[i].Created.Before(list[j].Created) })
	c.identities = list

	c.syncing = true
	names := []string{tr("Anonymous")}
	selected := uint(0)
	for i, meta := range list {
		names = append(names, meta.Name)
		if meta.ID == c.st.LastIdentity() {
			selected = uint(i + 1)
		}
	}
	c.combo.SetModel(gtk.NewStringList(names))
	c.combo.SetSelected(selected)
	c.syncing = false

	for _, r := range c.rows {
		c.group.Remove(r)
	}
	c.rows = nil
	if len(list) == 0 {
		empty := adw.NewActionRow()
		empty.SetTitle(tr("No certificates yet"))
		empty.SetSubtitle(tr("Without one you connect anonymously and cannot register on servers."))
		c.add(empty)
		return
	}
	for _, meta := range list {
		c.add(c.certRow(meta))
	}
}

func (c *certList) add(w gtk.Widgetter) {
	c.group.Add(w)
	c.rows = append(c.rows, w)
}

func (c *certList) certRow(meta certs.IdentityMeta) *adw.ActionRow {
	row := adw.NewActionRow()
	row.SetTitle(meta.Name)
	sub := fmt.Sprintf(tr("Created %s"), meta.Created.Local().Format("2006-01-02"))
	if ident, err := c.m.Load(meta.ID); err == nil && ident.Certificate != nil {
		sub += " · " + shortFingerprint(certs.Fingerprint(ident.Certificate))
	}
	if meta.Email != "" {
		sub += " · " + meta.Email
	}
	row.SetSubtitle(sub)
	row.AddPrefix(gtk.NewImageFromIconName("application-certificate-symbolic"))

	export := gtk.NewButtonFromIconName("document-save-symbolic")
	export.AddCSSClass("flat")
	export.SetVAlign(gtk.AlignCenter)
	export.SetTooltipText(tr("Export (back up) as PEM, including the private key"))
	export.ConnectClicked(func() {
		identities.Export(&c.pw.Window.Window, c.m, meta, func(err error) {
			if err != nil {
				c.pw.AddToast(adw.NewToast(fmt.Sprintf(tr("Export failed: %v"), err)))
				return
			}
			c.pw.AddToast(adw.NewToast(fmt.Sprintf(tr("Exported identity %s"), meta.Name)))
		})
	})
	del := gtk.NewButtonFromIconName("user-trash-symbolic")
	del.AddCSSClass("flat")
	del.SetVAlign(gtk.AlignCenter)
	del.SetTooltipText(tr("Delete certificate"))
	del.ConnectClicked(func() { c.confirmDelete(meta) })
	row.AddSuffix(export)
	row.AddSuffix(del)
	return row
}

func (c *certList) confirmDelete(meta certs.IdentityMeta) {
	alert := adw.NewAlertDialog(fmt.Sprintf(tr("Delete “%s”?"), meta.Name),
		tr("Servers on which you registered with this certificate will no longer recognize you. Export it first if you might need it again. This cannot be undone."))
	alert.AddResponse("cancel", tr("Cancel"))
	alert.AddResponse("delete", tr("Delete"))
	alert.SetResponseAppearance("delete", adw.ResponseDestructive)
	alert.SetDefaultResponse("cancel")
	alert.SetCloseResponse("cancel")
	alert.ConnectResponse(func(r string) {
		if r != "delete" {
			return
		}
		if err := c.m.Delete(meta.ID); err != nil {
			c.pw.AddToast(adw.NewToast(fmt.Sprintf(tr("Delete failed: %v"), err)))
			return
		}
		if c.st.LastIdentity() == meta.ID {
			c.st.SetLastIdentity("")
		}
		c.refresh()
		c.pw.AddToast(adw.NewToast(fmt.Sprintf(tr("Deleted identity %s"), meta.Name)))
	})
	alert.Present(c.pw)
}

// shortFingerprint shows the first bytes of a grouped-hex fingerprint.
func shortFingerprint(fp string) string {
	const shown = 8 // bytes
	parts := strings.FieldsFunc(fp, func(r rune) bool { return r == ':' || r == ' ' })
	if len(parts) > shown {
		parts = parts[:shown]
	}
	return strings.ToUpper(strings.Join(parts, ":")) + "…"
}

// --- Audio Input page ------------------------------------------------------

func audioInputPage(st settings.Settings, b *bus.Bus, sc Shortcuts, pw *adw.PreferencesWindow) *adw.PreferencesPage {
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

	echo := adw.NewSwitchRow()
	echo.SetTitle(tr("Echo Cancellation"))
	echo.SetSubtitle(tr("For speakers instead of a headset: keeps the others from hearing themselves through your microphone"))
	echo.SetActive(st.EchoCancellation())
	coreglib.InternObject(echo).NotifyProperty("active", func() {
		st.SetEchoCancellation(echo.Active())
	})
	devGroup.Add(echo)

	// When the microphone is sent.
	modes := []string{"voice-activation", "push-to-talk", "continuous"}
	modeRow := adw.NewComboRow()
	modeRow.SetTitle(tr("Transmit"))
	modeRow.SetModel(gtk.NewStringList([]string{tr("On Voice Activity"), tr("Push-to-Talk"), tr("Continuously")}))
	for i, m := range modes {
		if m == st.TransmissionMode() {
			modeRow.SetSelected(uint(i))
		}
	}
	modeHint := func() {
		switch st.TransmissionMode() {
		case "push-to-talk":
			modeRow.SetSubtitle(pushToTalkHint(sc))
		case "continuous":
			modeRow.SetSubtitle(tr("All the time, whatever the level"))
		default:
			modeRow.SetSubtitle(tr("When your voice is louder than the activation threshold"))
		}
	}
	modeHint()
	if sc != nil {
		pw.ConnectDestroy(sc.OnChange(modeHint))
	}
	modeGroup := adw.NewPreferencesGroup()
	modeGroup.SetTitle(tr("Sending"))
	modeGroup.Add(modeRow)

	vadGroup := adw.NewPreferencesGroup()
	vadGroup.SetTitle(tr("Voice Detection"))
	// The threshold only matters for voice activation.
	vadGroup.SetVisible(st.TransmissionMode() == "voice-activation")
	coreglib.InternObject(modeRow).NotifyProperty("selected", func() {
		if sel := int(modeRow.Selected()); sel < len(modes) {
			st.SetTransmissionMode(modes[sel])
			modeHint()
			vadGroup.SetVisible(modes[sel] == "voice-activation")
		}
	})

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
	page.Add(modeGroup)
	page.Add(vadGroup)
	page.Add(txGroup)
	return page
}

// pushToTalkHint names the push-to-talk key, or where to set one.
func pushToTalkHint(sc Shortcuts) string {
	if sc != nil {
		for _, b := range sc.Bindings() {
			if b.ID == shortcuts.PushToTalk && b.Trigger != "" {
				return fmt.Sprintf(tr("While %s is held"), b.Label())
			}
		}
		if available, known := sc.Available(); known && !available {
			return tr("Your desktop offers no global shortcuts, so push-to-talk has no key")
		}
	}
	return tr("No key yet: set it up under General → Global Shortcuts")
}

// shortcutsGroup lists the global shortcuts with the keys the desktop
// assigned, and a button that opens the desktop's dialog to set them.
func shortcutsGroup(sc Shortcuts, pw *adw.PreferencesWindow) *adw.PreferencesGroup {
	group := adw.NewPreferencesGroup()
	group.SetTitle(tr("Global Shortcuts"))
	group.SetDescription(tr("Work while another window has the focus. The keys are assigned in your desktop's dialog."))
	button := gtk.NewButton()
	button.AddCSSClass("flat")
	button.SetVAlign(gtk.AlignCenter)
	button.ConnectClicked(sc.SetUp)
	group.SetHeaderSuffix(button)

	rows := map[string]*gtk.Label{}
	for _, s := range sc.Shortcuts() {
		row := adw.NewActionRow()
		row.SetTitle(s.Description)
		key := gtk.NewLabel("")
		key.AddCSSClass("dim-label")
		row.AddSuffix(key)
		rows[s.ID] = key
		group.Add(row)
	}
	update := func() {
		available, known := sc.Available()
		switch {
		case known && !available:
			group.SetDescription(tr("Your desktop offers no global shortcuts (Global Shortcuts portal)."))
			button.SetVisible(false)
		case sc.IsSetUp():
			button.SetLabel(tr("Change…"))
		default:
			button.SetLabel(tr("Set Up…"))
		}
		button.SetSensitive(available && !sc.Busy())
		keys := map[string]string{}
		for _, b := range sc.Bindings() {
			keys[b.ID] = b.Label()
		}
		for id, l := range rows {
			if k := keys[id]; k != "" {
				l.SetText(k)
				l.RemoveCSSClass("dim-label")
			} else {
				l.AddCSSClass("dim-label")
				l.SetText(tr("Not assigned"))
			}
		}
	}
	update()
	remove := sc.OnChange(update)
	pw.ConnectDestroy(remove)
	return group
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

// --- Chat page -------------------------------------------------------------

func chatPage(st settings.Settings) *adw.PreferencesPage {
	page := adw.NewPreferencesPage()
	page.SetTitle(tr("Chat"))
	page.SetName("chat")
	page.SetIconName("chat-message-new-symbolic")

	group := adw.NewPreferencesGroup()
	group.SetTitle(tr("Channel Log"))
	group.SetDescription(tr("Events shown as faded lines between the messages of the channel chat"))
	for _, e := range chat.LogEvents {
		row := adw.NewSwitchRow()
		row.SetTitle(chat.LogLabel(e))
		row.SetActive(chat.LogEnabled(st.ChatLogEvents(), e))
		coreglib.InternObject(row).NotifyProperty("active", func() {
			ov := st.ChatLogEvents()
			if row.Active() == chat.LogDefault(e) {
				delete(ov, string(e))
			} else {
				ov[string(e)] = row.Active()
			}
			st.SetChatLogEvents(ov)
		})
		group.Add(row)
	}
	page.Add(group)
	return page
}

// --- Behavior page ---------------------------------------------------------

func generalPage(st settings.Settings, player *effects.Player, pw *adw.PreferencesWindow, sc Shortcuts) *adw.PreferencesPage {
	parent := func() *gtk.Window { return &pw.Window.Window }
	page := adw.NewPreferencesPage()
	page.SetTitle(tr("General"))
	page.SetName("general")
	page.SetIconName("preferences-other-symbolic")

	page.Add(languageGroup(st, pw))
	page.Add(appearanceGroup(st))

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
	if sc != nil {
		page.Add(shortcutsGroup(sc, pw))
	}
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

// colorSchemes are the color-scheme setting's values, in the combo's order.
var colorSchemes = []string{"system", "light", "dark"}

// appearanceGroup chooses the light or dark style (or the system's).
func appearanceGroup(st settings.Settings) *adw.PreferencesGroup {
	row := adw.NewComboRow()
	// TRANSLATORS: Appearance setting: light, dark or the system's color scheme.
	row.SetTitle(tr("Style"))
	row.SetSubtitle(tr("Light or dark; Follow System uses the desktop's setting"))
	row.SetModel(gtk.NewStringList([]string{tr("Follow System"), tr("Light"), tr("Dark")}))
	for i, v := range colorSchemes {
		if v == st.ColorScheme() {
			row.SetSelected(uint(i))
		}
	}
	coreglib.InternObject(row).NotifyProperty("selected", func() {
		if i := int(row.Selected()); i < len(colorSchemes) {
			st.SetColorScheme(colorSchemes[i])
		}
	})
	group := adw.NewPreferencesGroup()
	group.SetTitle(tr("Appearance"))
	group.Add(row)
	return group
}

// languageGroup overrides the interface language (default: the system's,
// English when Murmel has no translation for it). Applies on restart.
func languageGroup(st settings.Settings, pw *adw.PreferencesWindow) *adw.PreferencesGroup {
	codes := append([]string{"", "en"}, ui.Languages()...)
	names := []string{tr("Follow System"), ui.LanguageName("en")}
	for _, c := range codes[2:] {
		names = append(names, ui.LanguageName(c))
	}
	row := adw.NewComboRow()
	row.SetTitle(tr("Language"))
	row.SetSubtitle(tr("Follow System uses the desktop's language, or English if Murmel has no translation for it"))
	row.SetModel(gtk.NewStringList(names))
	for i, c := range codes {
		if c == st.Language() {
			row.SetSelected(uint(i))
		}
	}
	coreglib.InternObject(row).NotifyProperty("selected", func() {
		i := int(row.Selected())
		if i >= len(codes) || codes[i] == st.Language() {
			return
		}
		st.SetLanguage(codes[i])
		pw.AddToast(adw.NewToast(tr("Restart Murmel to use the new language.")))
	})
	group := adw.NewPreferencesGroup()
	group.Add(row)
	return group
}
