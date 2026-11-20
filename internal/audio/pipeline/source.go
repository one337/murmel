package pipeline

// Source yields 480-sample mono PCM frames (the audio device's capture
// side). Read blocks until a frame is available and returns io.EOF at
// end of stream. The returned slice is valid until the next Read.
type Source interface {
	Read() ([]int16, error)
}
