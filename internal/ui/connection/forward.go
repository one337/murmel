package connection

import (
	"io.github/murmel/internal/bus"
)

// forwardBus re-emits every event type the gumble bridge can produce from
// src into dst, preserving order (both buses dispatch via FIFO idle
// sources). onDisconnected — when non-nil — runs on the main loop after the
// forwarded Disconnected event, letting the controller tear exactly this
// connection's bus down. The returned stop function unsubscribes all
// forwarders.
func forwardBus(src, dst *bus.Bus, onDisconnected func()) (stop func()) {
	var stops []func()

	sub := func(f func()) {
		stops = append(stops, f)
	}

	sub(bus.Subscribe(src, func(ev bus.Connected) { dst.Emit(ev) }))
	sub(bus.Subscribe(src, func(ev bus.Disconnected) {
		dst.Emit(ev)
		if onDisconnected != nil {
			onDisconnected()
		}
	}))
	sub(bus.Subscribe(src, func(ev bus.TextMessage) { dst.Emit(ev) }))
	sub(bus.Subscribe(src, func(ev bus.UserAdded) { dst.Emit(ev) }))
	sub(bus.Subscribe(src, func(ev bus.UserRemoved) { dst.Emit(ev) }))
	sub(bus.Subscribe(src, func(ev bus.UserUpdated) { dst.Emit(ev) }))
	sub(bus.Subscribe(src, func(ev bus.ChannelAdded) { dst.Emit(ev) }))
	sub(bus.Subscribe(src, func(ev bus.ChannelRemoved) { dst.Emit(ev) }))
	sub(bus.Subscribe(src, func(ev bus.ChannelUpdated) { dst.Emit(ev) }))
	sub(bus.Subscribe(src, func(ev bus.PermissionDenied) { dst.Emit(ev) }))
	sub(bus.Subscribe(src, func(ev bus.ServerConfigUpdated) { dst.Emit(ev) }))
	// Replies to admin requests (ban list, registered users, channel ACL).
	sub(bus.Subscribe(src, func(ev bus.BanListFetched) { dst.Emit(ev) }))
	sub(bus.Subscribe(src, func(ev bus.UserListFetched) { dst.Emit(ev) }))
	sub(bus.Subscribe(src, func(ev bus.ACLFetched) { dst.Emit(ev) }))

	return func() {
		for _, s := range stops {
			s()
		}
	}
}
