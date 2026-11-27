package main

import (
	"bytes"
	"log"
	"log/slog"
	"os"
	"regexp"
	"strings"
	"testing"

	"github.com/one337/murmel/internal/ui/secrets/secretstest"
)

// The program links libsecret: no test may reach the desktop's keyring.
func TestMain(m *testing.M) {
	cleanup := secretstest.Isolate()
	code := m.Run()
	cleanup()
	os.Exit(code)
}

// logLines logs through a logHandler with the given G_MESSAGES_DEBUG and
// returns the written lines.
func logLines(t *testing.T, env string, log func(l *slog.Logger)) []string {
	t.Helper()
	var out bytes.Buffer
	log(slog.New(newLogHandler(&out, env)))
	s := strings.TrimSuffix(out.String(), "\n")
	if s == "" {
		return nil
	}
	return strings.Split(s, "\n")
}

// glibInfo logs like gotk4 does for a GLib info message (priority 6).
func glibInfo(l *slog.Logger) {
	l.Info("Vulkan: Loader Message", "priority", "6", "glib_domain", "Gdk")
}

func TestGLibInfoAndDebugAreHidden(t *testing.T) {
	got := logLines(t, "", func(l *slog.Logger) {
		glibInfo(l)
		l.Debug("details", "priority", "7", "glib_domain", "Gtk")
		l.Info("Remote error from secret service", "glib_old_log_api", "1", "priority", "6", "glib_domain", "libsecret")
	})
	if len(got) != 0 {
		t.Fatalf("GLib info/debug shown without G_MESSAGES_DEBUG: %q", got)
	}
}

func TestGLibWarningsAndMurmelMessagesStay(t *testing.T) {
	got := logLines(t, "", func(l *slog.Logger) {
		l.Warn("gdk_frame_timings_submitted() called on submitted frame.", "priority", "4", "glib_domain", "Gdk")
		l.Error("gtk_native_unrealize: assertion 'clock != NULL' failed", "priority", "4", "glib_domain", "Gtk")
		l.Info("connected", "addr", "example.org:64738")
		l.Debug("voice: outgoing packet dropped")
	})
	want := []string{
		`WARN gdk_frame_timings_submitted() called on submitted frame. priority=4 glib_domain=Gdk`,
		`ERROR gtk_native_unrealize: assertion 'clock != NULL' failed priority=4 glib_domain=Gtk`,
		`INFO connected addr=example.org:64738`,
	}
	if len(got) != len(want) {
		t.Fatalf("lines = %q, want %d lines", got, len(want))
	}
	stamp := regexp.MustCompile(`^\d{4}/\d\d/\d\d \d\d:\d\d:\d\d `)
	for i, line := range got {
		if !stamp.MatchString(line) {
			t.Errorf("line %d has no time stamp: %q", i, line)
		}
		if rest := stamp.ReplaceAllString(line, ""); rest != want[i] {
			t.Errorf("line %d = %q, want %q", i, rest, want[i])
		}
	}
}

func TestGMessagesDebugShowsGLibInfoAsDebug(t *testing.T) {
	for _, env := range []string{"all", "Gdk", "Gtk Gdk"} {
		got := logLines(t, env, glibInfo)
		if len(got) != 1 || !strings.Contains(got[0], " DEBUG Vulkan: Loader Message priority=6 glib_domain=Gdk") {
			t.Errorf("G_MESSAGES_DEBUG=%q: lines = %q, want the message as DEBUG", env, got)
		}
	}
	if got := logLines(t, "Gtk", glibInfo); len(got) != 0 {
		t.Errorf("G_MESSAGES_DEBUG=Gtk shows a Gdk message: %q", got)
	}
	// It is GLib's variable: Murmel's own debug messages stay hidden.
	if got := logLines(t, "all", func(l *slog.Logger) { l.Debug("voice: outgoing packet dropped") }); len(got) != 0 {
		t.Errorf("G_MESSAGES_DEBUG=all shows Murmel's debug messages: %q", got)
	}
}

func TestAttrsAndGroupsKeepTheirText(t *testing.T) {
	got := logLines(t, "", func(l *slog.Logger) {
		l.With("server", "a b").WithGroup("dial").Info("retry", "n", 2)
	})
	if len(got) != 1 || !strings.HasSuffix(got[0], ` INFO retry server="a b" dial.n=2`) {
		t.Fatalf("lines = %q", got)
	}
}

// Warnings written through the log package (gotk4 writes them that way)
// arrive at slog's info level; they are shown as WARN.
func TestLogPackageWarningsAreWarnings(t *testing.T) {
	var out bytes.Buffer
	prev, w, flags := slog.Default(), log.Writer(), log.Flags()
	slog.SetDefault(slog.New(newLogHandler(&out, "")))
	defer func() {
		// Setting slog's default back does not undo the log package's
		// redirection.
		slog.SetDefault(prev)
		log.SetOutput(w)
		log.SetFlags(flags)
	}()
	log.Printf("gotk4: warning: object %v cannot be resurrected for generated closure", "0x17fbdb10")
	log.Printf("warning: something")
	log.Printf("gotk4: object list updated")
	log.Printf("connect failed: warning: not a warning prefix")
	got := strings.Split(strings.TrimSuffix(out.String(), "\n"), "\n")
	want := []string{
		" WARN gotk4: warning: object 0x17fbdb10 cannot be resurrected for generated closure",
		" WARN warning: something",
		" INFO gotk4: object list updated",
		" INFO connect failed: warning: not a warning prefix",
	}
	if len(got) != len(want) {
		t.Fatalf("lines = %q", got)
	}
	for i := range want {
		if !strings.HasSuffix(got[i], want[i]) {
			t.Errorf("line %d = %q, want suffix %q", i, got[i], want[i])
		}
	}
}
