package pipeline

// Sink consumes 480-sample mono PCM frames (the audio device's playback
// side). The frame is only valid for the duration of the call:
// implementations that keep it must copy.
type Sink interface {
	Write(frame []int16) error
}
