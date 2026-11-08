package gumble

import (
	"time"
)

const (
	// AudioSampleRate is the audio sample rate (in hertz) for incoming and
	// outgoing audio.
	AudioSampleRate = 48000

	// AudioDefaultInterval is the default interval that audio packets are sent
	// at.
	AudioDefaultInterval = 10 * time.Millisecond

	// AudioDefaultFrameSize is the number of audio frames that should be sent in
	// a 10ms window.
	AudioDefaultFrameSize = AudioSampleRate / 100

	// AudioMaximumFrameSize is the maximum audio frame size from another user
	// that will be processed.
	AudioMaximumFrameSize = AudioSampleRate / 1000 * 60

	// AudioDefaultDataBytes is the default number of bytes that an audio frame
	// can use.
	AudioDefaultDataBytes = 40

	// AudioChannels is the number of audio channels that are contained in an
	// audio stream.
	AudioChannels = 1
)

// AudioListener is the interface that must be implemented by types wishing to
// receive incoming audio data from the server.
//
// OnAudioStream is called when an audio stream for a user starts. It is the
// implementer's responsibility to continuously process AudioStreamEvent.C
// until it is closed.
type AudioListener interface {
	OnAudioStream(e *AudioStreamEvent)
}

// AudioStreamEvent is event that is passed to AudioListener.OnAudioStream.
type AudioStreamEvent struct {
	Client *Client
	User   *User
	C      <-chan *AudioPacket
}

// AudioBuffer is a slice of PCM audio samples.
type AudioBuffer []int16

func (a AudioBuffer) writeAudio(client *Client, seq int64, final bool, X, Y, Z *float32) error {
	encoder := client.AudioEncoder
	if encoder == nil {
		return nil
	}
	// The byte budget follows the buffer length (20/40/60 ms packets) and,
	// when set, the encoder bitrate, so the encoder can reach its target
	// without exceeding it (P13).
	dataBytes := client.audioDataBytes(len(a))
	raw, err := encoder.Encode(a, len(a), dataBytes)
	if final {
		defer encoder.Reset()
	}
	if err != nil {
		return err
	}

	var targetID byte
	if target := client.VoiceTarget; target != nil {
		targetID = byte(target.ID)
	}
	return client.writeAudioPacket(byte(4), targetID, seq, final, raw, X, Y, Z)
}

// AudioPacket contains incoming audio samples and information.
type AudioPacket struct {
	Client *Client
	Sender *User
	Target *VoiceTarget

	AudioBuffer

	HasPosition bool
	X, Y, Z     float32
}

// AudioMaxPacketBytes caps one encoded voice packet so the whole UDP
// datagram stays within Mumble's 1024-byte voice buffer (P13).
const AudioMaxPacketBytes = 1000

// SetAudioBitrate makes the per-packet byte budget follow bitsPerSecond
// (0 restores Config.AudioDataBytes per 10 ms frame). Safe for concurrent
// use with an open AudioOutgoing stream.
func (c *Client) SetAudioBitrate(bitsPerSecond int) {
	c.audioBitrate.Store(int64(bitsPerSecond))
}

// audioDataBytes is the byte budget for one packet of samples samples.
func (c *Client) audioDataBytes(samples int) int {
	frames := max(1, samples/AudioDefaultFrameSize)
	n := c.Config.AudioDataBytes * frames
	if b := int(c.audioBitrate.Load()); b > 0 {
		// bits/s × seconds / 8, rounded up.
		n = (b*samples/AudioSampleRate + 7) / 8
	}
	return max(1, min(n, AudioMaxPacketBytes))
}
