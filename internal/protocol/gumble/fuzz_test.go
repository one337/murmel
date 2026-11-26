package gumble

import (
	"bytes"
	"encoding/binary"
	"net"
	"testing"
)

// packetStream frames the given bodies the way the server sends them.
func packetStream(bodies ...[]byte) []byte {
	var b []byte
	for i, body := range bodies {
		b = binary.BigEndian.AppendUint16(b, uint16(i))
		b = binary.BigEndian.AppendUint32(b, uint32(len(body)))
		b = append(b, body...)
	}
	return b
}

// FuzzReadPacket reads packets from an arbitrary byte stream until an
// error. Every packet is within the size limit, header and body are the
// next bytes of the stream, and reading stops at the end of it.
func FuzzReadPacket(f *testing.F) {
	f.Add(packetStream([]byte{8, 1}, nil, []byte("hello")))
	f.Add([]byte{0, 1, 0xff, 0xff, 0xff, 0xff, 0})
	f.Add([]byte{0, 1, 0, 0, 0x10, 0x01})
	f.Add(packetStream(make([]byte, 4097)))
	f.Fuzz(func(t *testing.T, stream []byte) {
		client, server := net.Pipe()
		defer func() { _ = client.Close() }()
		go func() {
			_, _ = server.Write(stream)
			_ = server.Close()
		}()
		conn := NewConn(client)
		// Keeps the fuzzer fast; the check is the same for any limit.
		conn.MaximumPacketBytes = 4096

		var read []byte
		for range len(stream)/6 + 1 {
			typ, body, err := conn.ReadPacket()
			if err != nil {
				if !bytes.HasPrefix(stream, read) {
					t.Fatalf("packets read %x are not a prefix of the stream %x", read, stream)
				}
				return
			}
			if len(body) > conn.MaximumPacketBytes {
				t.Fatalf("packet of %d bytes, limit %d", len(body), conn.MaximumPacketBytes)
			}
			read = binary.BigEndian.AppendUint16(read, typ)
			read = binary.BigEndian.AppendUint32(read, uint32(len(body)))
			read = append(read, body...)
			if !bytes.HasPrefix(stream, read) {
				t.Fatalf("packets read %x are not a prefix of the stream %x", read, stream)
			}
		}
		t.Fatalf("still reading after %d packets from %d bytes", len(stream)/6+1, len(stream))
	})
}
