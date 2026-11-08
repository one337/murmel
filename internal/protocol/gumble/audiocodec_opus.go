package gumble

import (
	adwopus "io.github/murmel/internal/audio/opus"
)

// RegisterMurmelOpusCodec wires the project's own Opus wrapper
// (internal/audio/opus, backed by gopkg.in/hraban/opus.v2 and system
// libopus) into this fork's global codec registry as codec type 4.
//
// Upstream gumble shipped this registration as an init() side effect in its
// layeh.com/gumble/opus subpackage, built on the unmaintained layeh.com/gopus
// (embedded Opus 1.1.2). That subpackage was dropped when vendoring; this
// call replaces it. Registration is explicit (no init) so callers control
// codec setup and imports stay side-effect free.
func RegisterMurmelOpusCodec() {
	RegisterAudioCodec(adwopus.CodecID, murmelOpusCodec{})
}

// murmelOpusCodec adapts the audio/opus constructors to this package's
// AudioCodec interface. The concrete *adwopus.Encoder and *adwopus.Decoder
// satisfy AudioEncoder/AudioDecoder structurally: identical signatures over
// built-in types only, so no per-method wrapping is needed.
type murmelOpusCodec struct{}

func (murmelOpusCodec) ID() int { return adwopus.CodecID }

func (murmelOpusCodec) NewEncoder() AudioEncoder {
	enc, err := adwopus.NewEncoder()
	if err != nil {
		return nil
	}
	return enc
}

func (murmelOpusCodec) NewDecoder() AudioDecoder {
	dec, err := adwopus.NewDecoder()
	if err != nil {
		return nil
	}
	return dec
}
