package opus

import (
	"fmt"

	hrabanopus "gopkg.in/hraban/opus.v2"
)

// AudioEncoder mirrors gumble.AudioEncoder (audiocodec.go) exactly.
type AudioEncoder interface {
	ID() int
	Encode(pcm []int16, frameSize, maxDataBytes int) ([]byte, error)
	Reset()
}

// AudioDecoder mirrors gumble.AudioDecoder (audiocodec.go) exactly.
type AudioDecoder interface {
	ID() int
	Decode(data []byte, frameSize int) ([]int16, error)
	Reset()
}

// AudioCodec mirrors gumble.AudioCodec: a generator for encoder/decoder
// pairs, registered once per codec ID.
type AudioCodec interface {
	ID() int
	NewEncoder() AudioEncoder
	NewDecoder() AudioDecoder
}

// CodecRegistry mirrors gumble's package-level registration hook
// (gumble.RegisterAudioCodec(id int, codec gumble.AudioCodec)) as an
// interface so registration can be routed into either upstream gumble or
// the vendored fork without this package importing either.
type CodecRegistry interface {
	RegisterAudioCodec(id int, codec AudioCodec)
}

// Encoder encodes mono 48 kHz PCM to Opus packets (VOIP application).
// It satisfies gumble.AudioEncoder.
type Encoder struct {
	enc     *hrabanopus.Encoder
	bitrate int
}

// NewEncoder returns an Opus VOIP encoder: 48 kHz mono, 32 kbps default
// target bitrate.
func NewEncoder() (*Encoder, error) {
	enc, err := hrabanopus.NewEncoder(SampleRate, Channels, hrabanopus.AppVoIP)
	if err != nil {
		return nil, fmt.Errorf("opus: new encoder: %w", err)
	}
	if err := enc.SetBitrate(BitrateDefault); err != nil {
		return nil, fmt.Errorf("opus: set default bitrate: %w", err)
	}
	return &Encoder{enc: enc, bitrate: BitrateDefault}, nil
}

// ID implements gumble.AudioEncoder.ID: Mumble codec type 4 (Opus).
func (*Encoder) ID() int { return CodecID }

// Encode implements gumble.AudioEncoder.Encode. pcm must hold a whole
// number of frameSize-sample frames (mono); the returned packet is at most
// maxDataBytes long.
func (e *Encoder) Encode(pcm []int16, frameSize, maxDataBytes int) ([]byte, error) {
	if frameSize <= 0 {
		return nil, fmt.Errorf("opus: frameSize %d must be > 0", frameSize)
	}
	if maxDataBytes <= 0 {
		return nil, fmt.Errorf("opus: maxDataBytes %d must be > 0", maxDataBytes)
	}
	if len(pcm) == 0 {
		return nil, fmt.Errorf("opus: no pcm data")
	}
	if len(pcm)%(frameSize*Channels) != 0 {
		return nil, fmt.Errorf("opus: pcm length %d not a multiple of frame size %d", len(pcm), frameSize*Channels)
	}
	data := make([]byte, maxDataBytes)
	n, err := e.enc.Encode(pcm, data)
	if err != nil {
		return nil, fmt.Errorf("opus: encode: %w", err)
	}
	return data[:n], nil
}

// Reset implements gumble.AudioEncoder.Reset: returns the encoder to a
// freshly initialized state, keeping the configured bitrate.
func (e *Encoder) Reset() {
	if err := e.enc.Reset(); err == nil {
		return
	}
	ne, nerr := hrabanopus.NewEncoder(SampleRate, Channels, hrabanopus.AppVoIP)
	if nerr != nil {
		return
	}
	if err := ne.SetBitrate(e.bitrate); err == nil {
		e.enc = ne
	}
}

// SetBitrate sets the target bitrate in bits per second, clamped by policy
// to the 8–64 kbps range Mumble traffic needs.
func (e *Encoder) SetBitrate(bitsPerSecond int) error {
	if bitsPerSecond < BitrateMin || bitsPerSecond > BitrateMax {
		return fmt.Errorf("opus: bitrate %d outside [%d, %d] bps", bitsPerSecond, BitrateMin, BitrateMax)
	}
	if err := e.enc.SetBitrate(bitsPerSecond); err != nil {
		return fmt.Errorf("opus: set bitrate: %w", err)
	}
	e.bitrate = bitsPerSecond
	return nil
}

// Bitrate returns the configured target bitrate in bits per second.
func (e *Encoder) Bitrate() int { return e.bitrate }

// SetInBandFEC enables in-band forward error correction in future packets
// (costs bitrate; makes DecodeFEC able to recover the frame before a lost
// one).
func (e *Encoder) SetInBandFEC(enabled bool) error {
	if err := e.enc.SetInBandFEC(enabled); err != nil {
		return fmt.Errorf("opus: set in-band FEC: %w", err)
	}
	return nil
}

// SetPacketLossPerc tells the encoder the expected packet loss percentage so
// it can size FEC redundancy accordingly.
func (e *Encoder) SetPacketLossPct(lossPerc int) error {
	if err := e.enc.SetPacketLossPerc(lossPerc); err != nil {
		return fmt.Errorf("opus: set packet loss percentage: %w", err)
	}
	return nil
}

// Decoder decodes Opus packets to mono 48 kHz PCM. It satisfies
// gumble.AudioDecoder.
type Decoder struct {
	dec *hrabanopus.Decoder
}

// NewDecoder returns an Opus decoder: 48 kHz mono.
func NewDecoder() (*Decoder, error) {
	dec, err := hrabanopus.NewDecoder(SampleRate, Channels)
	if err != nil {
		return nil, fmt.Errorf("opus: new decoder: %w", err)
	}
	return &Decoder{dec: dec}, nil
}

// ID implements gumble.AudioDecoder.ID: Mumble codec type 4 (Opus).
func (*Decoder) ID() int { return CodecID }

// Decode implements gumble.AudioDecoder.Decode. A nil or empty data slice
// triggers Packet Loss Concealment: instead of failing, the decoder
// synthesizes a full frame of audio from its internal state, which is how
// gumble callers paper over lost voice packets.
func (d *Decoder) Decode(data []byte, frameSize int) ([]int16, error) {
	if frameSize <= 0 {
		return nil, fmt.Errorf("opus: frameSize %d must be > 0", frameSize)
	}
	pcm := make([]int16, frameSize*Channels)
	if len(data) == 0 {
		if err := d.dec.DecodePLC(pcm); err != nil {
			return nil, fmt.Errorf("opus: PLC: %w", err)
		}
		return pcm, nil
	}
	n, err := d.dec.Decode(data, pcm)
	if err != nil {
		return nil, fmt.Errorf("opus: decode: %w", err)
	}
	if n <= 0 {
		return nil, fmt.Errorf("opus: decoder produced %d samples", n)
	}
	return pcm[:n*Channels], nil
}

// DecodeFEC is the extended decode: with fec=true it requests in-band FEC
// recovery from the packet (libopus falls back to concealment when the
// packet carries no FEC data); with fec=false it is identical to Decode.
// Empty data performs PLC regardless of fec.
func (d *Decoder) DecodeFEC(data []byte, frameSize int, fec bool) ([]int16, error) {
	if !fec || len(data) == 0 {
		return d.Decode(data, frameSize)
	}
	if frameSize <= 0 {
		return nil, fmt.Errorf("opus: frameSize %d must be > 0", frameSize)
	}
	pcm := make([]int16, frameSize*Channels)
	if err := d.dec.DecodeFEC(data, pcm); err != nil {
		return nil, fmt.Errorf("opus: FEC decode: %w", err)
	}
	return pcm, nil
}

// Reset implements gumble.AudioDecoder.Reset: returns the decoder to a
// freshly initialized state. hraban/opus does not expose
// opus_decoder_resetctl, so this re-creates the underlying decoder.
func (d *Decoder) Reset() {
	if nd, err := hrabanopus.NewDecoder(SampleRate, Channels); err == nil {
		d.dec = nd
	}
}

// generator is the AudioCodec implementation handed to registries.
type generator struct{}

// Codec is the Opus (type 4) codec generator, ready for registration via
// RegisterInto. It mirrors upstream gumble/opus's Codec generator but does
// not self-register.
var Codec AudioCodec = generator{}

func (generator) ID() int { return CodecID }

func (generator) NewEncoder() AudioEncoder {
	enc, err := NewEncoder()
	if err != nil {
		return nil
	}
	return enc
}

func (generator) NewDecoder() AudioDecoder {
	dec, err := NewDecoder()
	if err != nil {
		return nil
	}
	return dec
}

// RegisterInto plugs the Opus codec (type 4) into the given registry, e.g.
// gumble's global codec table or the vendored fork's equivalent. It is the
// only registration path: importing this package never mutates any global
// state. Callers using gumble adapt like so:
//
//	gumble.RegisterAudioCodec(opus.CodecID, gumbleCodec{opus.Codec})
//
// where gumbleCodec re-exports NewEncoder/NewDecoder with gumble's interface
// return types (the concrete Encoder/Decoder need no wrapping — they satisfy
// gumble.AudioEncoder/gumble.AudioDecoder structurally).
func RegisterInto(r CodecRegistry) {
	r.RegisterAudioCodec(CodecID, Codec)
}
