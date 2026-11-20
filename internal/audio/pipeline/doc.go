// Package pipeline holds what the voice path shares: the frame format
// (mono 48 kHz PCM16 in 10 ms frames of 480 samples), the Source and
// Sink contracts the audio device implements, the level scale of the
// meters and the threshold setting, and the voice activity detector.
//
// The VAD adapts to the noise floor (or uses the user's absolute
// threshold), opens an episode after OpenDelay above the open threshold,
// keeps it through CloseDelay of hangover and hands the frames before
// the start out as pre-roll. Sample does not allocate; it runs on the
// capture path.
package pipeline
