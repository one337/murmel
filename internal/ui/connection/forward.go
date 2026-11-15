package connection

import (
	"io.github/murmel/internal/bus"
)

// forwardBus re-emits every event type the gumble bridge can produce from
// src into dst, preserving order (both buses dispatch via FIFO idle
// sources). pass — when non-nil — is asked for every event (on the main
// loop) and drops it by returning false: the controller passes only the
// events of the current connection attempt. onDisconnected — when non-nil —
// runs on the main loop after a Disconnected event, letting the controller
// tear exactly this connection's bus down. The returned stop function
// unsubscribes all forwarders.
func forwardBus(src, dst *bus.Bus, pass func(bus.Event) bool, onDisconnected func()) (stop func()) {
	var stops []func()

	sub := func(f func()) {
		stops = append(stops, f)
	}
	emit := func(ev bus.Event) {
		if pass == nil || pass(ev) {
			dst.Emit(ev)
		}
	}

	sub(bus.Subscribe(src, func(ev bus.Connected) { emit(ev) }))
	sub(bus.Subscribe(src, func(ev bus.Disconnected) {
		emit(ev)
		if onDisconnected != nil {
			onDisconnected()
		}
	}))
	sub(bus.Subscribe(src, func(ev bus.TextMessage) { emit(ev) }))
	sub(bus.Subscribe(src, func(ev bus.UserAdded) { emit(ev) }))
	sub(bus.Subscribe(src, func(ev bus.UserRemoved) { emit(ev) }))
	sub(bus.Subscribe(src, func(ev bus.UserUpdated) { emit(ev) }))
	sub(bus.Subscribe(src, func(ev bus.ChannelAdded) { emit(ev) }))
	sub(bus.Subscribe(src, func(ev bus.ChannelRemoved) { emit(ev) }))
	sub(bus.Subscribe(src, func(ev bus.ChannelUpdated) { emit(ev) }))
	sub(bus.Subscribe(src, func(ev bus.PermissionDenied) { emit(ev) }))
	sub(bus.Subscribe(src, func(ev bus.ServerConfigUpdated) { emit(ev) }))
	// Replies to admin requests (ban list, registered users, channel ACL).
	sub(bus.Subscribe(src, func(ev bus.BanListFetched) { emit(ev) }))
	sub(bus.Subscribe(src, func(ev bus.UserListFetched) { emit(ev) }))
	sub(bus.Subscribe(src, func(ev bus.ACLFetched) { emit(ev) }))
	sub(bus.Subscribe(src, func(ev bus.UserStatsFetched) { emit(ev) }))

	return func() {
		for _, s := range stops {
			s()
		}
	}
}
