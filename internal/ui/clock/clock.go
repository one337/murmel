// Package clock formats times and dates for display: times follow the
// desktop's 12/24-hour setting, dates the regional format of the locale.
//
// Everything except Watch's background read runs on the GTK main loop.
package clock

import (
	"strings"
	"sync"
	"time"

	"github.com/diamondburned/gotk4/pkg/glib/v2"
)

// Clock formats as the desktop names them (org.gnome.desktop.interface
// clock-format).
const (
	format12h = "12h"
	format24h = "24h"
)

var (
	// desktop is the desktop's clock format; "" while unknown, in which
	// case the locale decides.
	desktop string

	listeners = map[uint64]func(){}
	nextID    uint64

	watchOnce sync.Once
	// watch starts reading the desktop's clock format and reports it,
	// and every later change, through set on the main loop. Tests swap it.
	watch = watchDesktop

	localeOnce sync.Once
	locale24h  bool
)

// Watch starts following the desktop's clock format. Call it once on the
// main loop at startup; it does not block. Until the format is known,
// times follow the locale.
func Watch() { watchOnce.Do(func() { watch(set) }) }

// OnChange calls f on the main loop whenever the clock format changes;
// remove stops that.
func OnChange(f func()) (remove func()) {
	nextID++
	id := nextID
	listeners[id] = f
	return func() { delete(listeners, id) }
}

// set takes a clock format from the desktop ("" or anything else:
// unknown) and tells the listeners when the result changed.
func set(format string) {
	if format != format12h && format != format24h {
		format = ""
	}
	before := uses24h()
	desktop = format
	if uses24h() == before {
		return
	}
	fs := make([]func(), 0, len(listeners))
	for _, f := range listeners {
		fs = append(fs, f)
	}
	for _, f := range fs {
		f()
	}
}

// Time formats the time of day of t, e.g. "15:04" or "3:04 PM".
func Time(t time.Time) string { return format(t, layout(uses24h())) }

// Date formats the date of t in the regional format, e.g. "15.11.2026".
func Date(t time.Time) string { return format(t, "%x") }

// layout is the strftime layout for the time of day.
func layout(h24 bool) string {
	if h24 {
		return "%R"
	}
	// %l has no leading zero; %p is empty in locales without AM/PM.
	return "%l:%M %p"
}

func uses24h() bool {
	switch desktop {
	case format24h:
		return true
	case format12h:
		return false
	}
	return localeUses24h()
}

// localeUses24h reports whether the locale writes the afternoon with a
// 24-hour clock: its time format (%X) shows 15:00 with the hour 15.
func localeUses24h() bool {
	localeOnce.Do(func() {
		afternoon := glib.NewDateTimeLocal(2000, 1, 1, 15, 0, 0).Format("%X")
		locale24h = strings.Contains(afternoon, "15")
	})
	return locale24h
}

// format renders t in local time; trimming drops the gap an empty %p or a
// space-padded %l leaves.
func format(t time.Time, layout string) string {
	return strings.TrimSpace(glib.NewDateTimeFromUnixLocal(t.Unix()).Format(layout))
}
