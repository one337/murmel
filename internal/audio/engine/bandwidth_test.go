package engine

import "testing"

func TestNetworkBandwidth(t *testing.T) {
	for _, tt := range []struct{ bitrate, frames, want int }{
		{40000, 1, 40000 + 37*8*100}, // 10 ms: 100 packets/s
		{40000, 2, 40000 + 37*8*50},  // 20 ms
		{40000, 6, 40000 + 37*8*100/6},
	} {
		if got := NetworkBandwidth(tt.bitrate, tt.frames); got != tt.want {
			t.Errorf("NetworkBandwidth(%d, %d) = %d, want %d", tt.bitrate, tt.frames, got, tt.want)
		}
	}
}

func TestEffectiveBitrate(t *testing.T) {
	for _, tt := range []struct {
		name                      string
		want, frames, server, out int
	}{
		{"no limit", 192000, 2, 0, 192000},
		{"fits", 40000, 2, 558000, 40000},
		{"clamped by server", 96000, 2, 72000, 72000 - 37*8*50},
		{"clamped to opus minimum", 40000, 1, 20000, 8000},
		{"above opus maximum", 300000, 2, 0, 192000},
	} {
		if got := EffectiveBitrate(tt.want, tt.frames, tt.server); got != tt.out {
			t.Errorf("%s: EffectiveBitrate = %d, want %d", tt.name, got, tt.out)
		}
	}
}
