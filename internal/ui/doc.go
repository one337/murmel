// Package ui implements the GTK4/Libadwaita user interface.
//
// The shell (this package's App and Window) owns the window chrome, the
// responsive layout regions, and the voice status bar. Region content is
// installed by later tasks through the Slot mount points (Window.Sidebar,
// Window.Chat); the connect dialog is attached via App.OnConnect.
//
// Threading: every widget call happens on the GLib main loop — either in
// signal handlers, in action handlers, or in bus event handlers (the bus
// dispatches via glib.IdleAdd by construction). Nothing in this package
// touches GTK from other goroutines.
//
// State: the shell never polls. All connection-state updates arrive through
// bus.Subscribe/SubscribeCtx subscriptions; widgets are the only state.
package ui
