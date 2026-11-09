package bus

import (
	"testing"

	"io.github/murmel/internal/protocol/gumble"
)

// TestCopyUserTexture: an avatar announced only by hash is pending until
// the image itself arrives.
func TestCopyUserTexture(t *testing.T) {
	u := &gumble.User{Session: 3, Name: "anna", TextureHash: []byte{1, 2, 3}}
	if info := copyUser(u); !info.TexturePending || info.Texture != nil {
		t.Fatalf("hash only: %+v", info)
	}
	u.Texture, u.TextureHash = []byte("png"), nil
	if info := copyUser(u); info.TexturePending || string(info.Texture) != "png" {
		t.Fatalf("with texture: %+v", info)
	}
	if info := copyUser(&gumble.User{Session: 4}); info.TexturePending || info.Texture != nil {
		t.Fatalf("no avatar: %+v", info)
	}
}
