package device

import (
	"fmt"

	"github.com/gen2brain/malgo"
)

// Info describes one enumerated audio device.
type Info struct {
	ID        malgo.DeviceID // opaque; feed back into Config
	Name      string
	IsDefault bool
}

// List is the enumeration result for the device settings UI: capture
// and playback lists, each with its default marked.
type List struct {
	Capture  []Info
	Playback []Info
}

// GetDevices enumerates capture and playback devices over a
// short-lived context (PulseAudio preferred, ALSA fallback). In
// headless environments — no sound server, no audio libraries — it
// returns a clean wrapped error; it never panics, aborts or hangs, so
// it is safe to call during application startup or from tests.
func GetDevices() (*List, error) {
	ctx, err := openContext(preferredBackends)
	if err != nil {
		return nil, fmt.Errorf("device: enumerate: init backends: %w", err)
	}
	defer ctx.Free()
	defer ctx.Uninit()

	caps, err := ctx.Devices(malgo.Capture)
	if err != nil {
		return nil, fmt.Errorf("device: enumerate capture: %w", err)
	}
	plays, err := ctx.Devices(malgo.Playback)
	if err != nil {
		return nil, fmt.Errorf("device: enumerate playback: %w", err)
	}
	l := &List{
		Capture:  make([]Info, 0, len(caps)),
		Playback: make([]Info, 0, len(plays)),
	}
	for i := range caps {
		l.Capture = append(l.Capture, Info{
			ID:        caps[i].ID,
			Name:      caps[i].Name(),
			IsDefault: caps[i].IsDefault != 0,
		})
	}
	for i := range plays {
		l.Playback = append(l.Playback, Info{
			ID:        plays[i].ID,
			Name:      plays[i].Name(),
			IsDefault: plays[i].IsDefault != 0,
		})
	}
	return l, nil
}

// Resolve maps the device names stored in the settings to a Config. Empty
// names, names that are no longer present (unplugged device) and a failed
// enumeration all fall back to the system default for that direction.
func Resolve(captureName, playbackName string) Config {
	var cfg Config
	if captureName == "" && playbackName == "" {
		return cfg
	}
	list, err := GetDevices()
	if err != nil {
		return cfg
	}
	cfg.CaptureID = findByName(list.Capture, captureName)
	cfg.PlaybackID = findByName(list.Playback, playbackName)
	return cfg
}

func findByName(infos []Info, name string) *malgo.DeviceID {
	if name == "" {
		return nil
	}
	for i := range infos {
		if infos[i].Name == name {
			id := infos[i].ID
			return &id
		}
	}
	return nil
}
