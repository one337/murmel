package gumble

import (
	"testing"

	"google.golang.org/protobuf/proto"

	"github.com/one337/murmel/internal/protocol/gumble/MumbleProto"
)

// deniedRecorder records permission-denied events; the embedded listener
// is never called.
type deniedRecorder struct {
	EventListener
	got []*PermissionDeniedEvent
}

func (r *deniedRecorder) OnPermissionDenied(e *PermissionDeniedEvent) { r.got = append(r.got, e) }

// Every kind of refusal the server can send reaches the listeners, also
// the ones this protocol version has no name for (newer servers).
func TestPermissionDeniedTypesReachListeners(t *testing.T) {
	for _, tt := range []struct {
		wire MumbleProto.PermissionDenied_DenyType
		want PermissionDeniedType
	}{
		{MumbleProto.PermissionDenied_Text, PermissionDeniedOther},
		{MumbleProto.PermissionDenied_H9K, PermissionDeniedH9K},
		{MumbleProto.PermissionDenied_ChannelCountLimit, PermissionDeniedChannelCountLimit},
		{12, PermissionDeniedChannelListenerLimit},
		{13, PermissionDeniedUserListenerLimit},
		{99, 99},
	} {
		client := &Client{Config: NewConfig(), Users: make(Users), Channels: make(Channels)}
		rec := &deniedRecorder{}
		client.Config.Attach(rec)
		wire := tt.wire
		buf, err := proto.Marshal(&MumbleProto.PermissionDenied{Type: &wire})
		if err != nil {
			t.Fatal(err)
		}
		if err := client.handlePermissionDenied(buf); err != nil {
			t.Errorf("type %d: handler failed: %v", tt.wire, err)
			continue
		}
		if len(rec.got) != 1 || rec.got[0].Type != tt.want {
			t.Errorf("type %d: listeners got %+v, want one event of type %d", tt.wire, rec.got, tt.want)
		}
	}
}
