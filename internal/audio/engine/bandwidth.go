package engine

import "io.github/murmel/internal/audio/opus"

// PacketOverheadBytes is the network cost of one voice packet beyond the
// Opus payload: IPv4 (20) + UDP (8) + OCB2-AES crypt header (4) + Mumble
// type/target byte (1) + sequence varint (2) + Opus length varint (2).
// The official client budgets the same items.
const PacketOverheadBytes = 37

// NetworkBandwidth returns the bits per second voice at bitrate needs on
// the wire with packetFrames 10 ms frames per packet.
func NetworkBandwidth(bitrate, packetFrames int) int {
	// 100/packetFrames packets per second, each carrying the overhead.
	return bitrate + PacketOverheadBytes*8*100/max(1, packetFrames)
}

// EffectiveBitrate lowers the wanted Opus bitrate until the voice stream
// fits the server's per-user bandwidth limit (serverMax in bit/s, 0 = no
// limit), never below opus.BitrateMin.
func EffectiveBitrate(want, packetFrames, serverMax int) int {
	want = max(opus.BitrateMin, min(opus.BitrateMax, want))
	if serverMax <= 0 || NetworkBandwidth(want, packetFrames) <= serverMax {
		return want
	}
	overhead := NetworkBandwidth(0, packetFrames)
	return max(opus.BitrateMin, serverMax-overhead)
}
