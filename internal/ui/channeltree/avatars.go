package channeltree

import (
	"github.com/diamondburned/gotk4/pkg/gdk/v4"
	"github.com/diamondburned/gotk4/pkg/glib/v2"

	"io.github/murmel/internal/bus"
)

// maxAvatarBytes bounds the avatar images decoded (servers limit uploads
// far below this).
const maxAvatarBytes = 4 << 20

// avatarEntry is a decoded avatar and the data it came from.
type avatarEntry struct {
	data *byte // identity of the decoded bytes (re-decode only on change)
	size int
	tex  gdk.Paintabler // nil: not decodable, the initials are shown
}

// applyAvatar shows a user's uploaded avatar in their row (adw.Avatar crops
// it to a square and rounds it like the initials it replaces), fetching it
// first if the server announced only its hash. Without one, or if it
// cannot be decoded, the row keeps the initials.
func (t *Tree) applyAvatar(r *userRowWidgets, u bus.UserInfo) {
	if u.TexturePending && !t.textureRequested[u.Session] {
		if t.textureRequested == nil {
			t.textureRequested = map[uint32]bool{}
		}
		t.textureRequested[u.Session] = true
		t.b.Emit(bus.UserTextureRequested{Session: u.Session})
	}
	tex := t.avatarFor(u)
	if tex == nil {
		r.avatar.SetCustomImage(nil)
		return
	}
	r.avatar.SetCustomImage(tex)
}

// avatarFor returns the decoded avatar for u, decoding only when its image
// changed.
func (t *Tree) avatarFor(u bus.UserInfo) gdk.Paintabler {
	if len(u.Texture) == 0 {
		delete(t.avatars, u.Session)
		return nil
	}
	if e, ok := t.avatars[u.Session]; ok && e.data == &u.Texture[0] && e.size == len(u.Texture) {
		return e.tex
	}
	e := avatarEntry{data: &u.Texture[0], size: len(u.Texture), tex: decodeAvatar(u.Texture)}
	if t.avatars == nil {
		t.avatars = map[uint32]avatarEntry{}
	}
	t.avatars[u.Session] = e
	return e.tex
}

// decodeAvatar decodes an uploaded avatar (PNG, JPEG, …); nil if it is not
// an image GTK can load (e.g. the raw pixels of pre-1.2 clients).
func decodeAvatar(data []byte) gdk.Paintabler {
	if len(data) == 0 || len(data) > maxAvatarBytes {
		return nil
	}
	tex, err := gdk.NewTextureFromBytes(glib.NewBytes(data))
	if err != nil || tex == nil {
		return nil
	}
	return tex
}
