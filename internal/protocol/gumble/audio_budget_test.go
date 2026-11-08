package gumble

import "testing"

func TestAudioDataBytes(t *testing.T) {
	c := &Client{Config: NewConfig()}
	// Without a bitrate: AudioDataBytes per 10 ms frame.
	if got := c.audioDataBytes(960); got != 2*AudioDefaultDataBytes {
		t.Fatalf("20 ms default budget = %d, want %d", got, 2*AudioDefaultDataBytes)
	}
	for _, tt := range []struct{ bitrate, samples, want int }{
		{40000, 480, 50},                    // 40 kb/s × 10 ms
		{40000, 960, 100},                   // 40 kb/s × 20 ms
		{192000, 960, 480},                  // 192 kb/s × 20 ms
		{192000, 2880, AudioMaxPacketBytes}, // 1440 bytes capped
	} {
		c.SetAudioBitrate(tt.bitrate)
		if got := c.audioDataBytes(tt.samples); got != tt.want {
			t.Errorf("%d bps, %d samples: %d bytes, want %d", tt.bitrate, tt.samples, got, tt.want)
		}
	}
}
