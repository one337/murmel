package clock

import (
	"strings"
	"sync"
	"testing"
	"time"
)

// The tests do not depend on installed locales: they check the layout
// chosen for each clock format and only locale-neutral parts of results.

var afternoon = time.Date(2026, 11, 15, 15, 4, 0, 0, time.Local)

func TestLayoutPerClockFormat(t *testing.T) {
	if got := layout(true); got != "%R" {
		t.Errorf("24h layout = %q", got)
	}
	if got := layout(false); got != "%l:%M %p" {
		t.Errorf("12h layout = %q", got)
	}
}

func TestTimeFollowsTheDesktopFormat(t *testing.T) {
	t.Cleanup(func() { desktop = "" })

	set(format24h)
	if got := Time(afternoon); got != "15:04" {
		t.Errorf("24h: Time = %q, want 15:04", got)
	}
	set(format12h)
	got := Time(afternoon)
	if !strings.HasPrefix(got, "3:04") {
		t.Errorf("12h: Time = %q, want 3:04 and the locale's afternoon mark", got)
	}
	if got != strings.TrimSpace(got) {
		t.Errorf("12h: Time = %q has surrounding space", got)
	}
}

// An empty %p (locales without AM/PM) or a padded %l must not leave a gap.
func TestFormatTrims(t *testing.T) {
	early := time.Date(2026, 11, 15, 9, 5, 0, 0, time.Local)
	if got := format(early, "%l:%M "); got != "9:05" {
		t.Errorf("format = %q, want 9:05", got)
	}
}

func TestDateUsesTheRegionalFormat(t *testing.T) {
	got := Date(afternoon)
	if got == "" || !strings.Contains(got, "15") {
		t.Errorf("Date = %q, want the day 15 in the locale's date format", got)
	}
}

func TestWatchReportsChanges(t *testing.T) {
	var report func(string)
	watch = func(set func(string)) { report = set }
	watchOnce = sync.Once{}
	t.Cleanup(func() {
		watch = watchDesktop
		watchOnce = sync.Once{}
		desktop = ""
	})

	calls := 0
	remove := OnChange(func() { calls++ })
	Watch()
	if report == nil {
		t.Fatal("Watch did not start the source")
	}

	// The locale decides first; report the opposite so a change happens.
	opposite := format12h
	if !localeUses24h() {
		opposite = format24h
	}
	report(opposite)
	if calls != 1 {
		t.Fatalf("listener called %d times after a change, want 1", calls)
	}
	report(opposite)
	if calls != 1 {
		t.Fatalf("listener called again without a change")
	}
	report("") // unknown again: back to the locale, which differs
	if calls != 2 {
		t.Fatalf("listener not called when the format became unknown")
	}

	remove()
	report(opposite)
	if calls != 2 {
		t.Fatal("listener called after remove")
	}
}
