package device

// data is the malgo duplex callback, invoked on the audio thread. Task-12
// discipline, statically enforced by grep on this file: the body only
// memcpys between the device buffers, two fixed staging buffers and the
// pre-allocated rings, plus one atomic counter for underflow stats.
//
// The backend decides the callback size: PeriodSizeInFrames is only a
// hint, and PipeWire/PulseAudio routinely deliver other sizes. Input is
// therefore re-chunked into whole 10 ms periods through capStage before
// it is pushed (drop-oldest when the consumer lags); output is served
// from playStage, refilled one period at a time from the playback ring.
// When the ring runs dry the rest of the buffer is zeroed so the device
// emits silence instead of stale audio. Both staging buffers are touched
// only by the audio thread.
func (d *Device) data(output, input []byte, frames uint32) {
	for len(input) > 0 {
		n := copy(d.capStage[d.capFill:], input)
		d.capFill += n
		input = input[n:]
		if d.capFill == frameBytes {
			d.capRing.Push(d.capStage[:])
			d.capFill = 0
		}
	}
	for len(output) > 0 {
		if d.playLeft == 0 {
			if !d.playRing.Pop(d.playStage[:]) {
				clear(output)
				d.underruns.Add(1)
				return
			}
			d.playLeft = frameBytes
		}
		n := copy(output, d.playStage[frameBytes-d.playLeft:])
		d.playLeft -= n
		output = output[n:]
	}
}
