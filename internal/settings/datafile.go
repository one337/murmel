package settings

import (
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"maps"
	"os"
	"path/filepath"
	"reflect"
	"sync"
	"time"
)

// Data file names under the data directory ($XDG_DATA_HOME/murmel).
const (
	serversFile    = "servers.json"
	localAudioFile = "local-audio.json"
)

// dataVersion is the format version the data files are written in. A file
// with a higher version comes from a newer Murmel: it is read as far as
// its known fields go and never overwritten.
const dataVersion = 1

// saveDelay is how long the data waits after the last change before it is
// written, so a burst of changes (a volume slider being dragged) is
// written once.
const saveDelay = 500 * time.Millisecond

type serversData struct {
	Version int           `json:"version"`
	Servers []ServerEntry `json:"servers"`
}

type localAudioData struct {
	Version int                `json:"version"`
	Volumes map[string]float64 `json:"volumes"`
	Mutes   map[string]bool    `json:"mutes"`
}

// versioned is data that is written with the current format version.
type versioned[T any] interface {
	current() T
}

func (d serversData) current() serversData       { d.Version = dataVersion; return d }
func (d localAudioData) current() localAudioData { d.Version = dataVersion; return d }

// Store is the process settings store: plain settings come from base
// (GSettings, or Memory without an installed schema); the saved servers
// and the per-user volumes and mutes, which grow with use, are kept in
// data files instead (servers.json and local-audio.json in the data
// directory). Values found in the old GSettings keys are moved there once.
//
// Changes to the data files are written saveDelay after the last one;
// Flush writes what is still pending and must run before the process
// exits. Changes to the data file values are not reported through
// ConnectChanged.
type Store struct {
	Settings

	servers *dataFile[serversData]
	audio   *dataFile[localAudioData]
}

// NewStore returns a Store over base whose data files live in dir; with
// dir "" they are kept in memory only.
func NewStore(base Settings, dir string) *Store {
	s := &Store{
		Settings: base,
		servers:  openDataFile[serversData](dir, serversFile),
		audio:    openDataFile[localAudioData](dir, localAudioFile),
	}

	gsServers := base.Servers()
	s.servers.load(func(d *serversData) {
		d.Servers = gsServers
	}, func(d serversData) bool {
		return serversEqual(d.Servers, gsServers)
	}, len(gsServers) > 0, func() {
		base.Reset("server-list")
	})

	gsVolumes, gsMutes := base.UserVolumes(), base.UserLocalMutes()
	s.audio.load(func(d *localAudioData) {
		d.Volumes, d.Mutes = gsVolumes, gsMutes
	}, func(d localAudioData) bool {
		return maps.Equal(d.Volumes, gsVolumes) && maps.Equal(d.Mutes, gsMutes)
	}, len(gsVolumes) > 0 || len(gsMutes) > 0, func() {
		base.Reset("user-volumes")
		base.Reset("user-local-mutes")
	})
	return s
}

func serversEqual(a, b []ServerEntry) bool {
	return len(a) == len(b) && (len(a) == 0 || reflect.DeepEqual(a, b))
}

func (s *Store) Servers() []ServerEntry {
	var out []ServerEntry
	s.servers.read(func(d *serversData) { out = append([]ServerEntry{}, d.Servers...) })
	return out
}

func (s *Store) SetServers(v []ServerEntry) {
	s.servers.change(func(d *serversData) { d.Servers = append([]ServerEntry{}, v...) })
}

func (s *Store) UserVolumes() map[string]float64 {
	out := map[string]float64{}
	s.audio.read(func(d *localAudioData) { maps.Copy(out, d.Volumes) })
	return out
}

func (s *Store) SetUserVolumes(v map[string]float64) {
	s.audio.change(func(d *localAudioData) { d.Volumes = maps.Clone(v) })
}

func (s *Store) UserLocalMutes() map[string]bool {
	out := map[string]bool{}
	s.audio.read(func(d *localAudioData) { maps.Copy(out, d.Mutes) })
	return out
}

func (s *Store) SetUserLocalMutes(v map[string]bool) {
	s.audio.change(func(d *localAudioData) { d.Mutes = maps.Clone(v) })
}

// Reset empties a data file value; other keys go to base.
func (s *Store) Reset(key string) {
	switch key {
	case "server-list":
		s.SetServers(nil)
	case "user-volumes":
		s.SetUserVolumes(nil)
	case "user-local-mutes":
		s.SetUserLocalMutes(nil)
	default:
		s.Settings.Reset(key)
	}
}

// Flush writes pending changes now. Call it before the process exits.
func (s *Store) Flush() {
	s.servers.flush()
	s.audio.flush()
}

// dataFile is one JSON data file with a version field, written atomically
// and after a delay.
type dataFile[T versioned[T]] struct {
	path  string // "" keeps the data in memory only
	delay time.Duration

	mu       sync.Mutex
	data     T
	readOnly bool // never write: a newer format, or a file that could not be moved aside
	dirty    bool
	timer    *time.Timer
}

func openDataFile[T versioned[T]](dir, name string) *dataFile[T] {
	f := &dataFile[T]{delay: saveDelay}
	if dir != "" {
		f.path = filepath.Join(dir, name)
	}
	return f
}

// load reads the file. Without one, the old GSettings values (fill) are
// moved into a new file when there are any (hasOld): the file is written,
// read back and compared (same), and only then the old values are
// cleared (clearOld).
func (f *dataFile[T]) load(fill func(*T), same func(T) bool, hasOld bool, clearOld func()) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.path == "" {
		fill(&f.data)
		return
	}

	data, err := os.ReadFile(f.path)
	switch {
	case err == nil:
		version, parsed, perr := decode[T](data)
		switch {
		case perr != nil:
			f.moveAside(perr)
		case version > dataVersion:
			slog.Warn("settings: data file from a newer version, it is not changed", "file", f.path, "version", version)
			f.data, f.readOnly = parsed, true
		default:
			f.data = parsed
			if hasOld {
				slog.Info("settings: data file in use, old GSettings values left as they are", "file", f.path)
			}
		}
		return
	case !errors.Is(err, os.ErrNotExist):
		f.moveAside(err)
		return
	}

	fill(&f.data)
	if !hasOld {
		return
	}
	if err := f.write(f.data); err != nil {
		slog.Warn("settings: moving values to the data file failed, they stay in GSettings", "file", f.path, "err", err)
		return
	}
	back, err := os.ReadFile(f.path)
	if err == nil {
		var version int
		var got T
		if version, got, err = decode[T](back); err == nil && (version != dataVersion || !same(got)) {
			err = errors.New("the file read back differs")
		}
	}
	if err != nil {
		slog.Warn("settings: moving values to the data file failed, they stay in GSettings", "file", f.path, "err", err)
		os.Remove(f.path)
		return
	}
	clearOld()
}

// moveAside renames an unreadable file to <name>.corrupt-<time>, so it is
// kept but never overwritten, and continues with empty data.
func (f *dataFile[T]) moveAside(cause error) {
	stamp := time.Now().Format("20060102-150405")
	aside := f.path + ".corrupt-" + stamp
	for i := 2; ; i++ {
		if _, err := os.Lstat(aside); err != nil {
			break // free (or not checkable: then the rename fails)
		}
		aside = fmt.Sprintf("%s.corrupt-%s-%d", f.path, stamp, i)
	}
	if err := os.Rename(f.path, aside); err != nil {
		slog.Error("settings: data file unreadable and cannot be moved aside; changes are not saved",
			"file", f.path, "err", cause, "rename", err)
		f.readOnly = true
		return
	}
	slog.Error("settings: data file unreadable, moved aside; starting empty", "file", f.path, "err", cause, "moved-to", aside)
}

// decode parses a data file: its version (at least 1) and its fields.
func decode[T any](data []byte) (int, T, error) {
	var head struct {
		Version int `json:"version"`
	}
	var v T
	if err := json.Unmarshal(data, &head); err != nil {
		return 0, v, err
	}
	if head.Version < 1 {
		return 0, v, fmt.Errorf("version %d", head.Version)
	}
	if err := json.Unmarshal(data, &v); err != nil && head.Version <= dataVersion {
		return 0, v, err
	}
	return head.Version, v, nil
}

func (f *dataFile[T]) read(fn func(*T)) {
	f.mu.Lock()
	defer f.mu.Unlock()
	fn(&f.data)
}

// change applies fn and schedules a write saveDelay after the last change.
func (f *dataFile[T]) change(fn func(*T)) {
	f.mu.Lock()
	defer f.mu.Unlock()
	fn(&f.data)
	if f.path == "" || f.readOnly {
		return
	}
	f.dirty = true
	if f.timer == nil {
		f.timer = time.AfterFunc(f.delay, f.flush)
	} else {
		f.timer.Reset(f.delay)
	}
}

func (f *dataFile[T]) flush() {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.timer != nil {
		f.timer.Stop()
		f.timer = nil
	}
	if !f.dirty {
		return
	}
	f.dirty = false
	if err := f.write(f.data); err != nil {
		slog.Error("settings: saving the data file failed", "file", f.path, "err", err)
	}
}

// write stores d with the current version, readable by the user only.
// Caller holds f.mu.
func (f *dataFile[T]) write(d T) error {
	out, err := json.MarshalIndent(d.current(), "", "  ")
	if err != nil {
		return err
	}
	return writeData(f.path, append(out, '\n'))
}

// writeData writes the file atomically (temporary file, then rename). It
// is a variable so tests can count or fail writes.
var writeData = func(path string, data []byte) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".tmp-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}

// dataDir is $XDG_DATA_HOME/murmel, or ~/.local/share/murmel.
func dataDir() (string, error) {
	if xdg := os.Getenv("XDG_DATA_HOME"); xdg != "" {
		return filepath.Join(xdg, "murmel"), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".local", "share", "murmel"), nil
}
