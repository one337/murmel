package shortcuts

import (
	"context"
	"errors"
	"os"
	"slices"
	"testing"
	"time"

	"github.com/one337/murmel/internal/ui/portal/portaltest"
)

func TestMain(m *testing.M) { os.Exit(portaltest.Run(m)) }

func TestUnavailableWithoutPortal(t *testing.T) {
	problems := portaltest.GLibProblems(t)
	if Available(context.Background()) {
		t.Error("Available without a portal")
	}
	if p := problems(); len(p) > 0 {
		t.Errorf("GLib logged %q", p)
	}
}

func TestUnavailableOnPortalWithoutShortcuts(t *testing.T) {
	problems := portaltest.GLibProblems(t)
	fake := &portaltest.Portal{Versions: map[string]uint32{"org.freedesktop.portal.Settings": 2}}
	fake.Serve(t)
	if Available(context.Background()) {
		t.Error("Available on a portal without GlobalShortcuts")
	}
	if p := problems(); len(p) > 0 {
		t.Errorf("GLib logged %q", p)
	}
}

func TestAvailableOnPortalWithShortcuts(t *testing.T) {
	fake := &portaltest.Portal{Versions: map[string]uint32{ifShortcuts: 1}}
	fake.Serve(t)
	if !Available(context.Background()) {
		t.Error("not Available on a portal with GlobalShortcuts")
	}
}

// bindCalls starts Bind against fake and returns the calls the portal got.
// The test portal never answers CreateSession's request, so Bind ends with
// the context.
func bindCalls(t *testing.T, fake *portaltest.Portal) []string {
	t.Helper()
	fake.Serve(t)
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	p := New("org.example.Test", []Shortcut{{ID: PushToTalk, Description: "Push to talk"}})
	if _, err := p.Bind(ctx); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Bind = %v, want the context's deadline", err)
	}
	return fake.Calls()
}

func TestNoRegistrationWithoutRegistry(t *testing.T) {
	if _, err := os.Stat("/.flatpak-info"); err == nil {
		t.Skip("inside Flatpak the app does not register")
	}
	problems := portaltest.GLibProblems(t)
	calls := bindCalls(t, &portaltest.Portal{Versions: map[string]uint32{ifShortcuts: 1}})
	if slices.Contains(calls, ifRegistry+".Register") {
		t.Errorf("registered with a portal that has no registry: %q", calls)
	}
	if !slices.Contains(calls, ifShortcuts+".CreateSession") {
		t.Errorf("no session requested: %q", calls)
	}
	if p := problems(); len(p) > 0 {
		t.Errorf("GLib logged %q", p)
	}
}

func TestRegistersWithRegistry(t *testing.T) {
	if _, err := os.Stat("/.flatpak-info"); err == nil {
		t.Skip("inside Flatpak the app does not register")
	}
	calls := bindCalls(t, &portaltest.Portal{Versions: map[string]uint32{ifShortcuts: 1, ifRegistry: 1}})
	if !slices.Contains(calls, ifRegistry+".Register") {
		t.Errorf("no registration with the portal's registry: %q", calls)
	}
}
