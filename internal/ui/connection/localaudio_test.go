package connection

import (
	"testing"

	"io.github/murmel/internal/bus"
	"io.github/murmel/internal/settings"
)

func TestUserAudioKey(t *testing.T) {
	withCert := bus.UserInfo{Name: "Frank", CertHash: "ab12"}
	if got := userAudioKey("Voice.Example.org:64738", withCert); got != "voice.example.org:64738|cert:ab12" {
		t.Errorf("certificate key = %q", got)
	}
	noCert := bus.UserInfo{Name: "Frank"}
	if got := userAudioKey("voice.example.org:64738", noCert); got != "voice.example.org:64738|name:Frank" {
		t.Errorf("name key = %q", got)
	}
}

func TestLocalAudioIsRememberedAndApplied(t *testing.T) {
	st := settings.NewMemory()
	c := &Controller{settings: st, appBus: bus.New(nil)}
	frank := bus.UserInfo{Session: 7, Name: "Frank", CertHash: "ab12"}
	c.rememberUser(frank)

	// Before the connection is up nothing is stored.
	c.onUserLocalAudioRequested(bus.UserLocalAudioRequested{Session: 7, Volume: 0.5})
	if len(st.UserVolumes()) != 0 {
		t.Fatal("stored a volume without a server")
	}

	c.audio.server = "example.org:64738"
	c.onUserLocalAudioRequested(bus.UserLocalAudioRequested{Session: 7, Volume: 0.5, Muted: true})
	key := "example.org:64738|cert:ab12"
	if st.UserVolumes()[key] != 0.5 || !st.UserLocalMutes()[key] {
		t.Fatalf("not stored: %v %v", st.UserVolumes(), st.UserLocalMutes())
	}

	// Back to 100 % and unmuted: the entries go away again.
	c.onUserLocalAudioRequested(bus.UserLocalAudioRequested{Session: 7, Volume: 1})
	if len(st.UserVolumes()) != 0 || len(st.UserLocalMutes()) != 0 {
		t.Fatalf("defaults kept as entries: %v %v", st.UserVolumes(), st.UserLocalMutes())
	}

	// Out-of-range volumes are clamped.
	c.onUserLocalAudioRequested(bus.UserLocalAudioRequested{Session: 7, Volume: 5})
	if st.UserVolumes()[key] != 2 {
		t.Fatalf("volume not clamped: %v", st.UserVolumes())
	}
}
