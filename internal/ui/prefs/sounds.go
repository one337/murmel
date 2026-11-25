package prefs

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"

	"github.com/diamondburned/gotk4-adwaita/pkg/adw"
	"github.com/diamondburned/gotk4/pkg/gio/v2"
	"github.com/diamondburned/gotk4/pkg/glib/v2"
	"github.com/diamondburned/gotk4/pkg/gtk/v4"

	"github.com/one337/murmel/internal/audio/effects"
	"github.com/one337/murmel/internal/i18n"
	"github.com/one337/murmel/internal/settings"
	"github.com/one337/murmel/internal/ui/gfileio"
	"github.com/one337/murmel/internal/ui/weakref"
)

// eventLabel is the preferences title of each sound event.
func eventLabel(ev effects.Event) string {
	switch ev {
	case effects.SelfConnected:
		return i18n.Tr("Connected to a server")
	case effects.SelfDisconnected:
		return i18n.Tr("Disconnected")
	case effects.SelfMoved:
		return i18n.Tr("You switched channels")
	case effects.UserJoinedChannel:
		return i18n.Tr("User joined your channel")
	case effects.UserLeftChannel:
		return i18n.Tr("User left your channel")
	case effects.UserConnected:
		return i18n.Tr("User connected to the server")
	case effects.UserDisconnected:
		return i18n.Tr("User disconnected from the server")
	case effects.PrivateMessage:
		return i18n.Tr("Private message")
	case effects.ChannelMessage:
		return i18n.Tr("Channel message")
	case effects.PushToTalkPressed:
		return i18n.Tr("Push-to-talk pressed")
	case effects.PushToTalkRelease:
		return i18n.Tr("Push-to-talk released")
	}
	return string(ev)
}

// builtinLabel names a built-in sound.
func builtinLabel(id string) string {
	switch id {
	case "join":
		return i18n.Tr("Rising chime")
	case "leave":
		return i18n.Tr("Falling chime")
	case "connect":
		return i18n.Tr("Rising triad")
	case "disconnect":
		return i18n.Tr("Falling triad")
	case "move":
		return i18n.Tr("Pop")
	case "blip-up":
		return i18n.Tr("Blip up")
	case "blip-down":
		return i18n.Tr("Blip down")
	case "message":
		return i18n.Tr("Bell")
	case "tick":
		return i18n.Tr("Tick")
	}
	return id
}

// chooseFile is the sentinel choice of the "Choose File…" item.
const chooseFile = "\x00choose"

// soundsRow builds the "Event sounds" expander: its switch is the
// sounds-enabled key, each child row picks the sound of one event (off,
// built-in, or an audio file) and has a preview button.
// soundsDir holds copies of sound files picked from locations without a
// local path; pw shows problems with a picked file.
func soundsRow(st settings.Settings, player *effects.Player, parent func() *gtk.Window, pw weakref.Ref[*adw.PreferencesDialog], soundsDir string) *adw.ExpanderRow {
	exp := adw.NewExpanderRow()
	exp.SetTitle(i18n.Tr("Event sounds"))
	exp.SetSubtitle(i18n.Tr("For connections, users, messages and push-to-talk"))
	exp.SetShowEnableSwitch(true)
	exp.SetEnableExpansion(st.SoundsEnabled())
	onNotify(exp, "enable-expansion", func(exp *adw.ExpanderRow) {
		st.SetSoundsEnabled(exp.EnableExpansion())
	})
	for _, ev := range effects.Events {
		exp.AddRow(soundEventRow(st, player, parent, pw, soundsDir, ev))
	}
	return exp
}

func soundEventRow(st settings.Settings, player *effects.Player, parent func() *gtk.Window, pw weakref.Ref[*adw.PreferencesDialog], soundsDir string, ev effects.Event) *adw.ComboRow {
	row := adw.NewComboRow()
	row.SetTitle(eventLabel(ev))

	var (
		choices  []string // parallel to the model items
		current  string   // the stored choice
		updating bool     // suppresses the notify handler while rebuilding
	)
	preview := gtk.NewButtonFromIconName("media-playback-start-symbolic")
	preview.SetTooltipText(i18n.Tr("Play"))
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
		old := current
		ov := st.SoundEvents()
		if choice == effects.Default(ev) {
			delete(ov, string(ev))
		} else {
			ov[string(ev)] = choice
		}
		st.SetSoundEvents(ov)
		current = choice
		if old != choice {
			removeUnusedCopy(soundsDir, old, ov)
		}
		preview.SetSensitive(current != effects.Off)
	}
	// rebuild fills the model for the current choice and selects it.
	rowRef := weakref.New(row)
	rebuild := func() {
		row, ok := rowRef.Get()
		if !ok {
			return
		}
		preview.SetSensitive(current != effects.Off)
		updating = true
		defer func() { updating = false }()
		choices = []string{effects.Off}
		labels := []string{i18n.Tr("Off")}
		for _, id := range effects.Builtins {
			choices = append(choices, effects.BuiltinChoice(id))
			labels = append(labels, builtinLabel(id))
		}
		if path, ok := effects.FilePath(current); ok {
			choices = append(choices, current)
			labels = append(labels, filepath.Base(path))
		}
		choices = append(choices, chooseFile)
		labels = append(labels, i18n.Tr("Choose File…"))
		row.SetModel(gtk.NewStringList(labels))
		for i, c := range choices {
			if c == current {
				row.SetSelected(uint(i))
			}
		}
	}
	current = effects.Choice(st.SoundEvents(), ev)
	rebuild()

	onNotify(row, "selected", func(row *adw.ComboRow) {
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
		owner, ok := pw.Get()
		if !ok {
			return
		}
		pickSoundFile(parent(), owner, soundsDir, func(path string, problem string) {
			if path != "" {
				store(effects.FileChoice(path))
				player.PlayChoice(current, st.AudioOutputDevice())
			}
			if problem != "" {
				t := adw.NewToast(problem)
				t.SetUseMarkup(false)
				pw.Do(func(pw *adw.PreferencesDialog) { pw.AddToast(t) })
			}
			rebuild() // also reverts the selection on cancel or a problem
		})
	})

	return row
}

// pickSoundFile asks for a WAV or Ogg Vorbis file and checks that it can
// be played. done receives its path, or a problem to show ("" for both when
// cancelled). A file without a local path (network location) is copied to
// soundsDir, since playback reads a local file. owner is the widget that
// asked; closing it abandons the pick.
func pickSoundFile(parent *gtk.Window, owner gtk.Widgetter, soundsDir string, done func(path, problem string)) {
	audio := gtk.NewFileFilter()
	audio.SetName(i18n.Tr("Sounds (WAV, Ogg Vorbis)"))
	for _, s := range []string{"wav", "ogg", "oga"} {
		audio.AddSuffix(s)
	}
	for _, m := range []string{"audio/x-wav", "audio/wav", "audio/ogg", "audio/x-vorbis+ogg"} {
		audio.AddMIMEType(m)
	}
	filters := gio.NewListStore(gtk.GTypeFileFilter)
	filters.Append(audio.Object)

	fd := gtk.NewFileDialog()
	fd.SetTitle(i18n.Tr("Choose Sound"))
	fd.SetModal(true)
	fd.SetFilters(filters)
	fd.SetDefaultFilter(audio)
	fd.Open(context.Background(), parent, func(res gio.AsyncResulter) {
		file, err := fd.OpenFinish(res)
		if err != nil || file == nil {
			done("", "")
			return
		}
		name, path := file.Basename(), file.Path()
		ctx := gfileio.ForWidget(owner)
		gfileio.Load(ctx, file, gfileio.MaxSoundBytes, func(data []byte, err error) {
			if err != nil {
				slog.Info("sound file not read", "file", name, "err", err)
				done("", gfileio.Message(err))
				return
			}
			go func() {
				_, decodeErr := effects.Decode(data)
				var copyErr error
				if decodeErr == nil && path == "" {
					path, copyErr = copySound(soundsDir, name, data)
				}
				glib.IdleAdd(func() {
					if ctx.Err() != nil {
						return
					}
					switch {
					case decodeErr != nil:
						slog.Info("sound file not usable", "file", name, "err", decodeErr)
						done("", i18n.Tr("This file is not a usable sound (WAV or Ogg Vorbis)."))
					case copyErr != nil:
						slog.Info("sound file not copied", "file", name, "err", copyErr)
						done("", copyErr.Error())
					default:
						done(path, "")
					}
				})
			}()
		})
	})
}

// copySound stores data under soundsDir with name (made unique) and
// returns the new path.
func copySound(soundsDir, name string, data []byte) (string, error) {
	if err := os.MkdirAll(soundsDir, 0o700); err != nil {
		return "", err
	}
	ext := filepath.Ext(name)
	stem := strings.TrimSuffix(filepath.Base(name), ext)
	for i := 1; ; i++ {
		path := filepath.Join(soundsDir, stem+ext)
		if i > 1 {
			path = filepath.Join(soundsDir, fmt.Sprintf("%s-%d%s", stem, i, ext))
		}
		f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
		if errors.Is(err, os.ErrExist) {
			continue
		}
		if err != nil {
			return "", err
		}
		if _, err := f.Write(data); err != nil {
			f.Close()
			os.Remove(path)
			return "", err
		}
		return path, f.Close()
	}
}

// removeUnusedCopy deletes the copy behind a replaced choice when it lies
// in soundsDir and no event uses it any more (overrides: the stored
// choices).
func removeUnusedCopy(soundsDir, choice string, overrides map[string]string) {
	path, ok := effects.FilePath(choice)
	if !ok || filepath.Dir(filepath.Clean(path)) != filepath.Clean(soundsDir) {
		return
	}
	for _, c := range overrides {
		if c == choice {
			return
		}
	}
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		slog.Info("sound copy not removed", "err", err)
	}
}
