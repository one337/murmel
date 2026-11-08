package gumble

import (
	"encoding/binary"
	"math"
	"sync/atomic"
)

// audioDispatchQueueSize bounds how many raw incoming audio packets may wait
// for the decode goroutine. At the default 10 ms packet interval this is
// ~2.5 s of audio; when the queue is full, incoming voice packets are
// dropped (voice is lossy — dropping beats stalling the TCP read loop).
const audioDispatchQueueSize = 256

// audioJob is one unit of work for the per-connection audio dispatch
// goroutine.
type audioJob struct {
	// gone marks a user-disconnect job: close the user's stream channels
	// and release the per-user decoder instead of decoding anything.
	gone bool

	user   *User
	codec  AudioCodec // codec resolved on the read loop when the job was queued
	target byte

	// payload holds the Opus frame bytes optionally followed by 3x
	// little-endian float32 positional data; audioLength is the length of
	// the Opus frame itself. The bytes are copied out of the read loop's
	// reusable buffer before queueing.
	payload     []byte
	audioLength int
}

// audioDispatchRoutine decodes queued incoming audio packets and dispatches
// them to the registered AudioListeners. Running this work off
// Client.readRoutine keeps Opus decoding (and slow listeners) from blocking
// TCP control-channel reads — gumble listener callbacks used to run on, and
// stall, the network read loop (upstream issue #38).
func (c *Client) audioDispatchRoutine() {
	for job := range c.audioQueue {
		if job.gone {
			c.closeUserAudio(job.user)
			continue
		}
		c.dispatchAudio(job)
	}
	// The connection is over, so no further packets can arrive: close
	// every remaining per-user stream so listener range loops terminate
	// (covers gone jobs dropped on a full queue too).
	for item := c.Config.AudioListeners.head; item != nil; item = item.next {
		for user, ch := range item.streams {
			delete(item.streams, user)
			close(ch)
		}
	}
}

// closeUserAudio releases everything held for a disconnected user's
// incoming audio: the per-listener stream channels are closed (upstream
// never closed them, leaving listener goroutines ranging over a dead
// channel and the streams map growing without bound) and the per-user Opus
// decoder (cgo state) is dropped so it can be garbage collected.
//
// Must only run on the audio dispatch goroutine, which owns every stream
// channel send — closing here cannot race an in-flight send.
func (c *Client) closeUserAudio(user *User) {
	// Decoder first: it is cleared before the channel closes so a
	// listener observing the closed stream also observes decoder == nil.
	user.decoder = nil
	c.volatile.Lock()
	for item := c.Config.AudioListeners.head; item != nil; item = item.next {
		if ch, ok := item.streams[user]; ok {
			delete(item.streams, user)
			close(ch)
		}
	}
	c.volatile.Unlock()
}

// dispatchAudio decodes a single queued packet and hands it to the audio
// listeners. The body is moved from handleUDPTunnel; per-user ordering is
// preserved because a single goroutine drains the queue.
func (c *Client) dispatchAudio(job *audioJob) {
	decoder := job.user.decoder
	if decoder == nil {
		// TODO: decoder pool
		if job.codec == nil {
			return
		}
		decoder = job.codec.NewDecoder()
		job.user.decoder = decoder
	}

	pcm, err := decoder.Decode(job.payload[:job.audioLength], AudioMaximumFrameSize)
	if err != nil {
		return
	}

	event := AudioPacket{
		Client: c,
		Sender: job.user,
		Target: &VoiceTarget{
			ID: uint32(job.target),
		},
		AudioBuffer: AudioBuffer(pcm),
	}

	if len(job.payload)-job.audioLength == 3*4 {
		// the packet has positional audio data; 3x float32
		buffer := job.payload[job.audioLength:]

		event.X = math.Float32frombits(binary.LittleEndian.Uint32(buffer))
		event.Y = math.Float32frombits(binary.LittleEndian.Uint32(buffer[4:]))
		event.Z = math.Float32frombits(binary.LittleEndian.Uint32(buffer[8:]))
		event.HasPosition = true
	}

	c.volatile.Lock()
	for item := c.Config.AudioListeners.head; item != nil; item = item.next {
		c.volatile.Unlock()
		ch := item.streams[job.user]
		if ch == nil {
			ch = make(chan *AudioPacket)
			item.streams[job.user] = ch
			streamEvent := AudioStreamEvent{
				Client: c,
				User:   job.user,
				C:      ch,
			}
			item.listener.OnAudioStream(&streamEvent)
		}
		ch <- &event
		c.volatile.Lock()
	}
	c.volatile.Unlock()
}

// AudioDropped returns the number of incoming voice packets that had to be
// dropped because the audio dispatch queue was full.
func (c *Client) AudioDropped() uint32 {
	return atomic.LoadUint32(&c.audioDropped)
}
