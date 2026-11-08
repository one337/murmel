package prefs

import (
	"context"
	"path/filepath"

	"github.com/diamondburned/gotk4-adwaita/pkg/adw"
	coreglib "github.com/diamondburned/gotk4/pkg/core/glib"
	"github.com/diamondburned/gotk4/pkg/gio/v2"
	"github.com/diamondburned/gotk4/pkg/gtk/v4"

	"io.github/murmel/internal/audio/effects"
	"io.github/murmel/internal/settings"
)

// eventLabel is the preferences title of each sound event.
func eventLabel(ev effects.Event) string {
	switch ev {
	case effects.SelfConnected:
		return tr("Connected to a server")
	case effects.SelfDisconnected:
		return tr("Disconnected")
	case effects.SelfMoved:
		return tr("You switched channels")
	case effects.UserJoinedChannel:
		return tr("User joined your channel")
	case effects.UserLeftChannel:
		return tr("User left your channel")
	case effects.UserConnected:
		return tr("User connected to the server")
	case effects.UserDisconnected:
		return tr("User disconnected from the server")
	case effects.PrivateMessage:
		return tr("Private message")
	case effects.ChannelMessage:
		return tr("Channel message")
	}
	return string(ev)
}

// builtinLabel names a built-in sound.
func builtinLabel(id string) string {
	switch id {
	case "join":
		return tr("Rising chime")
	case "leave":
		return tr("Falling chime")
	case "connect":
		return tr("Rising triad")
	case "disconnect":
		return tr("Falling triad")
	case "move":
		return tr("Pop")
	case "blip-up":
		return tr("Blip up")
	case "blip-down":
		return tr("Blip down")
	case "message":
		return tr("Bell")
	case "tick":
		return tr("Tick")
	}
	return id
}

// chooseFile is the sentinel choice of the "Choose File…" item.
const chooseFile = "\x00choose"

// soundsRow builds the "Sounds" expander: its switch is the
// sounds-enabled key, each child row picks the sound of one event (off,
// built-in, or an audio file) and has a preview button.
func soundsRow(st settings.Settings, player *effects.Player, parent func() *gtk.Window) *adw.ExpanderRow {
	exp := adw.NewExpanderRow()
	exp.SetTitle(tr("Sounds"))
	exp.SetSubtitle(tr("Play a sound for connection, channel and message events"))
	exp.SetShowEnableSwitch(true)
	exp.SetEnableExpansion(st.SoundsEnabled())
	coreglib.InternObject(exp).NotifyProperty("enable-expansion", func() {
		st.SetSoundsEnabled(exp.EnableExpansion())
	})
	for _, ev := range effects.Events {
		exp.AddRow(soundEventRow(st, player, parent, ev))
	}
	return exp
}

func soundEventRow(st settings.Settings, player *effects.Player, parent func() *gtk.Window, ev effects.Event) *adw.ComboRow {
	row := adw.NewComboRow()
	row.SetTitle(eventLabel(ev))

	var (
		choices  []string // parallel to the model items
		current  string   // the stored choice
		updating bool     // suppresses the notify handler while rebuilding
	)
	preview := gtk.NewButtonFromIconName("media-playback-start-symbolic")
	preview.SetTooltipText(tr("Play"))
	preview.SetVAlign(gtk.AlignCenter)
	preview.AddCSSClass("flat")
	preview.ConnectClicked(func() {
		if current == effects.Off {
			return
		}
		player.PlayChoice(current, st.AudioOutputDevice())
	})
	row.AddSuffix(preview)

	store := func(choice string) {
		ov := st.SoundEvents()
		if choice == effects.Default(ev) {
			delete(ov, string(ev))
		} else {
			ov[string(ev)] = choice
		}
		st.SetSoundEvents(ov)
		current = choice
		preview.SetSensitive(current != effects.Off)
	}
	// rebuild fills the model for the current choice and selects it.
	rebuild := func() {
		preview.SetSensitive(current != effects.Off)
		updating = true
		defer func() { updating = false }()
		choices = []string{effects.Off}
		labels := []string{tr("Off")}
		for _, id := range effects.Builtins {
			choices = append(choices, effects.BuiltinChoice(id))
			labels = append(labels, builtinLabel(id))
		}
		if path, ok := effects.FilePath(current); ok {
			choices = append(choices, current)
			labels = append(labels, filepath.Base(path))
		}
		choices = append(choices, chooseFile)
		labels = append(labels, tr("Choose File…"))
		row.SetModel(gtk.NewStringList(labels))
		for i, c := range choices {
			if c == current {
				row.SetSelected(uint(i))
			}
		}
	}
	current = effects.Choice(st.SoundEvents(), ev)
	rebuild()

	coreglib.InternObject(row).NotifyProperty("selected", func() {
		if updating {
			return
		}
		sel := int(row.Selected())
		if sel >= len(choices) {
			return
		}
		if choices[sel] != chooseFile {
			store(choices[sel])
			return
		}
		pickSoundFile(parent(), func(path string) {
			if path != "" {
				store(effects.FileChoice(path))
				player.PlayChoice(current, st.AudioOutputDevice())
			}
			rebuild() // also reverts the selection on cancel
		})
	})

	return row
}

// pickSoundFile asks for a WAV or Ogg Vorbis file; done receives its path
// ("" when cancelled).
func pickSoundFile(parent *gtk.Window, done func(path string)) {
	audio := gtk.NewFileFilter()
	audio.SetName(tr("Sounds (WAV, Ogg Vorbis)"))
	for _, s := range []string{"wav", "ogg", "oga"} {
		audio.AddSuffix(s)
	}
	for _, m := range []string{"audio/x-wav", "audio/wav", "audio/ogg", "audio/x-vorbis+ogg"} {
		audio.AddMIMEType(m)
	}
	filters := gio.NewListStore(gtk.GTypeFileFilter)
	filters.Append(audio.Object)

	fd := gtk.NewFileDialog()
	fd.SetTitle(tr("Choose Sound"))
	fd.SetModal(true)
	fd.SetFilters(filters)
	fd.SetDefaultFilter(audio)
	fd.Open(context.Background(), parent, func(res gio.AsyncResulter) {
		file, err := fd.OpenFinish(res)
		if err != nil || file == nil {
			done("")
			return
		}
		done(file.Path())
	})
}
