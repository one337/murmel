package bus

import "sync"

// ConnectionState is the bus's view of the connection lifecycle.
type ConnectionState int

const (
	// StateDisconnected means there is no connection (also the initial
	// state before the first Connected event).
	StateDisconnected ConnectionState = iota
	// StateConnected means a server connection is established and synced.
	StateConnected
)

// State is an immutable snapshot of the bus-maintained world state. The
// channel/user tree is derivable: ChannelInfo.ParentID and UserInfo.Channel
// form the tree; users per channel are the UserInfo entries with that
// channel ID.
type State struct {
	Connection  ConnectionState
	SelfSession uint32
	// LastError is the text of the most recent ConnectError ("" once a
	// subsequent Connected event arrives).
	LastError string
	Channels  map[uint32]ChannelInfo
	Users     map[uint32]UserInfo
	// MaxMessageLength and MaxImageMessageLength are the server's limits
	// for text (also channel descriptions) and for text with images, in
	// bytes; 0 while the server has not announced them.
	MaxMessageLength      int
	MaxImageMessageLength int
}

// store is the single-writer state store. The bus pump goroutine (fed by
// gumble events) is the only writer; readers take a snapshot, ideally from
// main-loop handlers.
type store struct {
	mu    sync.RWMutex
	state State
}

func newStore() *store {
	return &store{state: State{
		Channels: make(map[uint32]ChannelInfo),
		Users:    make(map[uint32]UserInfo),
	}}
}

// apply folds one event into the state. Connected carries the full initial
// channel/user tree because the fork suppresses channel/user events until the
// initial sync completes — the bridge seeds the tree from the client's maps
// when OnConnect fires. Disconnected drops the tree: the server state is gone.
// Unknown/derived-only events (VoiceActivity, LatencyStats, TextMessage,
// PermissionDenied) carry no world-state and are ignored.
func (s *store) apply(ev Event) {
	s.mu.Lock()
	defer s.mu.Unlock()

	switch e := ev.(type) {
	case Connected:
		s.state.Connection = StateConnected
		s.state.SelfSession = e.SelfSession
		s.state.LastError = ""
		s.state.Channels = make(map[uint32]ChannelInfo, len(e.Channels))
		for _, ch := range e.Channels {
			if len(ch.Links) > 0 {
				ch.Links = append([]uint32(nil), ch.Links...)
			}
			s.state.Channels[ch.ID] = ch
		}
		s.state.Users = make(map[uint32]UserInfo, len(e.Users))
		for _, u := range e.Users {
			s.state.Users[u.Session] = u
		}
	case Disconnected:
		s.state.Connection = StateDisconnected
		s.state.Channels = make(map[uint32]ChannelInfo)
		s.state.Users = make(map[uint32]UserInfo)
		s.state.MaxMessageLength, s.state.MaxImageMessageLength = 0, 0
	case ServerConfigUpdated:
		if e.MaxMessageLength != nil {
			s.state.MaxMessageLength = *e.MaxMessageLength
		}
		if e.MaxImageMessageLength != nil {
			s.state.MaxImageMessageLength = *e.MaxImageMessageLength
		}
	case ConnectError:
		s.state.Connection = StateDisconnected
		s.state.LastError = e.Text
	case ChannelAdded:
		s.putChannel(e.Channel)
	case ChannelRemoved:
		delete(s.state.Channels, e.ID)
	case ChannelUpdated:
		s.putChannel(e.Channel)
	case UserAdded:
		s.state.Users[e.User.Session] = e.User
	case UserRemoved:
		delete(s.state.Users, e.Session)
	case UserUpdated:
		s.state.Users[e.User.Session] = e.User
	}
}

func (s *store) putChannel(ch ChannelInfo) {
	if len(ch.Links) > 0 {
		ch.Links = append([]uint32(nil), ch.Links...)
	}
	s.state.Channels[ch.ID] = ch
}

// snapshot returns a deep copy; the caller may retain and mutate it freely.
func (s *store) snapshot() State {
	s.mu.RLock()
	defer s.mu.RUnlock()

	snap := State{
		Connection:  s.state.Connection,
		SelfSession: s.state.SelfSession,
		LastError:   s.state.LastError,
		Channels:    make(map[uint32]ChannelInfo, len(s.state.Channels)),
		Users:       make(map[uint32]UserInfo, len(s.state.Users)),

		MaxMessageLength:      s.state.MaxMessageLength,
		MaxImageMessageLength: s.state.MaxImageMessageLength,
	}
	for id, ch := range s.state.Channels {
		if len(ch.Links) > 0 {
			ch.Links = append([]uint32(nil), ch.Links...)
		}
		snap.Channels[id] = ch
	}
	for id, u := range s.state.Users {
		snap.Users[id] = u
	}
	return snap
}
