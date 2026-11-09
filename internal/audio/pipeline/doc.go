// Package pipeline provides a pluggable, headless-testable audio pipeline
// for Mumble voice traffic: mono 48 kHz PCM16 in 10 ms frames (480 samples,
// 100 frames/s everywhere).
//
// # Graph
//
//	Send side:  Source → [capture stage: pool, taps] → queue →
//	            [encode stage] → PacketSink
//	Recv side:  PacketSource → [decode stage] → FrameRelay → [sink stage] → Sink
//
// The capture stage runs taps (VAD, level meter) synchronously on the
// capture goroutine — taps must not block or allocate. The FrameRelay
// between decode and sink is the seam where the jitter buffer
// will be inserted; the default relay is a bounded drop-oldest queue.
//
// # Zero allocation hot path
//
// Frames are fixed-size structs ([480]int16 payload) recycled through a
// pre-allocated pool; inter-stage handoff moves pointers only. Encode and
// decode callbacks receive pre-owned buffers. The Opus wrapper
// (internal/audio/opus) allocates its output buffers itself; that cost is
// codec-side and recorded separately in the task-11 benchmark evidence.
//
// # Device adapter
//
// Source is designed so a malgo capture callback adapts thinly: the
// callback copies its input buffer into the next pooled frame and hands it
// to the same tap/queue path used here (the device Source will wrap the
// pipeline's capture stage internals, not this package's test sources).
//
// # Overflow policy
//
// Every bounded queue drops the oldest queued frame when full (voice
// traffic is delay-sensitive; stale frames are worthless), recycles it to
// the pool, and counts the drop in Stats.
package pipeline
