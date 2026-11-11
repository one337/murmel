package connection

import (
	"strings"

	"io.github/murmel/internal/bus"
)

// Local volume and mute of other users: only for oneself, remembered per
// user and server (settings user-volumes and user-local-mutes) and applied
// whenever that user is on the server. A user is recognised by the
// certificate (across name changes), otherwise by name. All of this runs
// on the main loop.

// localAudio is the controller's per-connection view of the users whose
// local audio can be remembered.
type localAudio struct {
	server string                  // "host:port" of the connection, "" while not connected
	users  map[uint32]bus.UserInfo // by session
}

// userAudioKey names user u on server for the settings.
func userAudioKey(server string, u bus.UserInfo) string {
	if u.CertHash != "" {
		return strings.ToLower(server) + "|cert:" + u.CertHash
	}
	return strings.ToLower(server) + "|name:" + u.Name
}

func (c *Controller) subscribeLocalAudio() {
	bus.SubscribeCtx(c.appCtx, c.appBus, func(ev bus.Connected) {
		c.audio.users = map[uint32]bus.UserInfo{}
		for _, u := range ev.Users {
			c.audio.users[u.Session] = u
		}
		c.applyLocalAudio()
	})
	bus.SubscribeCtx(c.appCtx, c.appBus, func(bus.Disconnected) {
		c.audio = localAudio{}
	})
	bus.SubscribeCtx(c.appCtx, c.appBus, func(ev bus.UserAdded) {
		c.rememberUser(ev.User)
		c.applyLocalAudioTo(ev.User)
	})
	bus.SubscribeCtx(c.appCtx, c.appBus, func(ev bus.UserUpdated) { c.rememberUser(ev.User) })
	bus.SubscribeCtx(c.appCtx, c.appBus, func(ev bus.UserRemoved) { delete(c.audio.users, ev.Session) })
	bus.SubscribeCtx(c.appCtx, c.appBus, c.onUserLocalAudioRequested)
}

func (c *Controller) rememberUser(u bus.UserInfo) {
	if c.audio.users == nil {
		c.audio.users = map[uint32]bus.UserInfo{}
	}
	c.audio.users[u.Session] = u
}

// startLocalAudio is called once the connection to server is up (and the
// voice engine listens): the remembered settings apply from now on.
func (c *Controller) startLocalAudio(server string) {
	c.audio.server = server
	c.applyLocalAudio()
}

func (c *Controller) applyLocalAudio() {
	for _, u := range c.audio.users {
		c.applyLocalAudioTo(u)
	}
}

// applyLocalAudioTo applies u's remembered volume and mute, if any.
func (c *Controller) applyLocalAudioTo(u bus.UserInfo) {
	if c.audio.server == "" {
		return
	}
	key := userAudioKey(c.audio.server, u)
	vol, hasVol := c.settings.UserVolumes()[key]
	muted := c.settings.UserLocalMutes()[key]
	if !hasVol && !muted {
		return
	}
	if !hasVol {
		vol = 1
	}
	c.emitLocalAudio(u.Session, vol, muted)
}

func (c *Controller) onUserLocalAudioRequested(ev bus.UserLocalAudioRequested) {
	u, ok := c.audio.users[ev.Session]
	if !ok || c.audio.server == "" {
		return
	}
	vol := min(max(ev.Volume, 0), 2)
	key := userAudioKey(c.audio.server, u)
	volumes := c.settings.UserVolumes()
	if vol == 1 {
		delete(volumes, key)
	} else {
		volumes[key] = vol
	}
	c.settings.SetUserVolumes(volumes)
	mutes := c.settings.UserLocalMutes()
	if ev.Muted {
		mutes[key] = true
	} else {
		delete(mutes, key)
	}
	c.settings.SetUserLocalMutes(mutes)
	c.emitLocalAudio(ev.Session, vol, ev.Muted)
}

// emitLocalAudio tells the voice engine the effective gain and the UI the
// state.
func (c *Controller) emitLocalAudio(session uint32, vol float64, muted bool) {
	gain := vol
	if muted {
		gain = 0
	}
	c.appBus.Emit(bus.UserVolumeChanged{Session: session, Volume: gain})
	c.appBus.Emit(bus.UserLocalAudioChanged{Session: session, Volume: vol, Muted: muted})
}
