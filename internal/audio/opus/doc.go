// Package opus wraps gopkg.in/hraban/opus.v2 (system libopus via cgo) behind
// the codec interface used by gumble (layeh.com/gumble/gumble audiocodec.go):
//
//	AudioEncoder: ID() int
//	              Encode(pcm []int16, frameSize, maxDataBytes int) ([]byte, error)
//	              Reset()
//	AudioDecoder: ID() int
//	              Decode(data []byte, frameSize int) ([]int16, error)
//	              Reset()
//
// The wrapper speaks Mumble's audio format: mono, 48 kHz, 10 ms frames
// (480 samples), Opus VOIP application, codec type 4. The Encoder and Decoder
// concrete types satisfy gumble.AudioEncoder and gumble.AudioDecoder
// structurally; compile-time assertions against upstream gumble live in
// codec_test.go.
//
// # Build prerequisite
//
// hraban/opus.v2 links against system libopus through cgo. Building or testing
// this package requires:
//
//	pkg-config --exists opus   # i.e. libopus-dev (Debian) / libopus-devel (Fedora)
//
// No OGG/file handling, no multistream, no resampling: mono 48 kHz only, per
// the Mumble protocol.
//
// # Registration
//
// Upstream gumble's own opus package registers itself into a global registry
// via init(). This package is side-effect free instead: call RegisterInto with
// a gumble-compatible registry to plug the codec in for Opus type 4 (see
// RegisterInto). Glue code that imports a gumble implementation adapts the
// local interfaces with zero conversion, since all method signatures use only
// built-in types.
package opus

// Codec type 4 in Mumble's codec registry (gumble audioCodecIDOpus).
const CodecID = 4

const (
	// SampleRate is Mumble's fixed Opus sample rate: 48 kHz.
	SampleRate = 48000
	// Channels is Mumble's Opus channel count: mono.
	Channels = 1
	// FrameSize is the default frame size: 480 samples = 10 ms at 48 kHz.
	FrameSize = 480

	// BitrateDefault is the default encoder target: 40 kbps, the official
	// Mumble client's default quality.
	BitrateDefault = 40 * 1000
	// BitrateMin is the lowest encoder bitrate: 8 kbps (servers with a low
	// bandwidth limit may force it this far down).
	BitrateMin = 8 * 1000
	// BitrateMax is the highest encoder bitrate: 192 kbps, the official
	// client's maximum quality.
	BitrateMax = 192 * 1000

	// MaxDataBytes is the largest encoded 10 ms frame at BitrateMax
	// (192 kbps × 10 ms = 240 bytes), plus header room.
	MaxDataBytes = 256
)
