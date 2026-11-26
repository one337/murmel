package main

import (
	"bytes"
	"context"
	"io"
	"log/slog"
	"strings"
	"sync"
)

// gotk4 passes every GLib message to slog.Default and logs GLib's info
// messages at slog's info level, so library chatter such as Vulkan loader
// details ends up in Murmel's log. GLib itself shows info and debug
// messages only for the domains named in G_MESSAGES_DEBUG ("all" for
// every domain); logHandler does the same and shows them as DEBUG.
//
// slog's own default handler cannot be wrapped: once a different handler
// is the default, the log package writes through it, and the default
// handler writes back into the log package. logHandler therefore writes
// itself, in the default handler's format: "2006/01/02 15:04:05 INFO msg
// key=value".
type logHandler struct {
	mu    *sync.Mutex
	out   io.Writer
	buf   *bytes.Buffer // attribute text, written by attrs; guarded by mu
	attrs slog.Handler  // a text handler for the attributes only
	glib  glibDebug
}

// glibDebug is the parsed G_MESSAGES_DEBUG.
type glibDebug struct {
	all     bool
	domains map[string]bool
}

func parseGLibDebug(env string) glibDebug {
	d := glibDebug{domains: map[string]bool{}}
	for f := range strings.FieldsSeq(env) {
		if f == "all" {
			d.all = true
		}
		d.domains[f] = true
	}
	return d
}

func (d glibDebug) shows(domain string) bool { return d.all || d.domains[domain] }

func (d glibDebug) any() bool { return d.all || len(d.domains) > 0 }

func newLogHandler(out io.Writer, gmessagesDebug string) *logHandler {
	buf := new(bytes.Buffer)
	return &logHandler{
		mu:  new(sync.Mutex),
		out: out,
		buf: buf,
		attrs: slog.NewTextHandler(buf, &slog.HandlerOptions{
			Level: slog.LevelDebug,
			ReplaceAttr: func(groups []string, a slog.Attr) slog.Attr {
				if len(groups) == 0 && (a.Key == slog.TimeKey || a.Key == slog.LevelKey || a.Key == slog.MessageKey) {
					return slog.Attr{}
				}
				return a
			},
		}),
		glib: parseGLibDebug(gmessagesDebug),
	}
}

// Enabled lets debug records through only while G_MESSAGES_DEBUG names a
// domain, for GLib's debug messages; Murmel's own stay hidden (Handle).
func (h *logHandler) Enabled(_ context.Context, l slog.Level) bool {
	return l >= slog.LevelInfo || h.glib.any()
}

func (h *logHandler) Handle(ctx context.Context, r slog.Record) error {
	level := r.Level
	if prio, domain, ok := glibPriority(r); ok {
		if prio == "6" || prio == "7" { // GLib info and debug
			if !h.glib.shows(domain) {
				return nil
			}
			level = slog.LevelDebug
		}
	} else if level < slog.LevelInfo {
		return nil
	} else if level == slog.LevelInfo && isLogWarning(r.Message) {
		level = slog.LevelWarn
	}

	h.mu.Lock()
	defer h.mu.Unlock()
	h.buf.Reset()
	attrs := slog.NewRecord(r.Time, r.Level, "", 0)
	r.Attrs(func(a slog.Attr) bool {
		attrs.AddAttrs(a)
		return true
	})
	if err := h.attrs.Handle(ctx, attrs); err != nil {
		return err
	}
	var line strings.Builder
	line.WriteString(r.Time.Format("2006/01/02 15:04:05"))
	line.WriteByte(' ')
	line.WriteString(level.String())
	line.WriteByte(' ')
	line.WriteString(r.Message)
	if a := strings.TrimSuffix(h.buf.String(), "\n"); a != "" {
		line.WriteByte(' ')
		line.WriteString(a)
	}
	line.WriteByte('\n')
	_, err := io.WriteString(h.out, line.String())
	return err
}

// isLogWarning reports whether msg is a warning written through the log
// package, which reaches slog at the info level: "warning: …" or, as
// gotk4 writes them, "gotk4: warning: …".
func isLogWarning(msg string) bool {
	if strings.HasPrefix(msg, "warning:") {
		return true
	}
	prefix, rest, ok := strings.Cut(msg, ": ")
	return ok && !strings.ContainsAny(prefix, " :") && strings.HasPrefix(rest, "warning:")
}

// glibPriority returns the syslog priority and domain of a GLib message
// (gotk4 adds them as attributes "priority" and "glib_domain").
func glibPriority(r slog.Record) (prio, domain string, ok bool) {
	r.Attrs(func(a slog.Attr) bool {
		switch a.Key {
		case "priority":
			prio, ok = a.Value.String(), true
		case "glib_domain":
			domain = a.Value.String()
		}
		return true
	})
	return prio, domain, ok
}

func (h *logHandler) WithAttrs(as []slog.Attr) slog.Handler {
	c := *h
	c.attrs = h.attrs.WithAttrs(as)
	return &c
}

func (h *logHandler) WithGroup(name string) slog.Handler {
	c := *h
	c.attrs = h.attrs.WithGroup(name)
	return &c
}
