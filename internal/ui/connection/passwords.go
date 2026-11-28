package connection

import (
	"errors"
	"sync"

	coreglib "github.com/diamondburned/gotk4/pkg/core/glib"
	"github.com/diamondburned/gotk4/pkg/gtk/v4"

	"github.com/one337/murmel/internal/bus"
	"github.com/one337/murmel/internal/i18n"
	"github.com/one337/murmel/internal/ui/secrets"
)

// passwordProblem returns what to tell the user after saving a password
// (or, with removing, removing one) failed with err; "" when there is
// nothing to tell: it worked, or there is no keyring, which the dialogs
// already say.
func passwordProblem(err error, removing bool) string {
	if err == nil || errors.Is(err, secrets.ErrNoKeyring) {
		return ""
	}
	var fileErr *secrets.FileError
	if errors.As(err, &fileErr) {
		if removing {
			return i18n.Tr("Saved password not removed: the password file could not be changed.")
		}
		return i18n.Tr("Password not saved: the password file could not be changed.")
	}
	switch secrets.KindOf(err) {
	case secrets.Refused:
		if removing {
			return i18n.Tr("Saved password not removed: the keyring is locked.")
		}
		return i18n.Tr("Password not saved: the keyring is locked.")
	case secrets.Gone:
		if removing {
			return i18n.Tr("Saved password not removed: no keyring found.")
		}
		return i18n.Tr("Password not saved: no keyring found.")
	}
	if removing {
		return i18n.Tr("Saved password not removed: the keyring reported an error.")
	}
	return i18n.Tr("Password not saved: the keyring reported an error.")
}

// announce asks screen readers to say msg. Without an accessibility bus
// GTK falls back to a context that cannot announce and crashes when asked
// to (GTK 4.24), so it is only asked where a screen reader can listen.
func announce(w announcer, msg string) {
	ctx := w.AtContext()
	if ctx == nil || coreglib.BaseObject(ctx).Type().Name() == "GtkTestATContext" {
		return
	}
	w.Announce(msg, gtk.AccessibleAnnouncementPriorityMedium)
}

// announcer is a widget screen readers can be told something about.
type announcer interface {
	AtContext() gtk.ATContexter
	Announce(message string, priority gtk.AccessibleAnnouncementPriority)
}

// noticeLeftPasswords says once per start, as soon as it is known, that
// passwords are still in the unencrypted file although there is a keyring.
func (c *Controller) noticeLeftPasswords() {
	var once sync.Once
	check := func() {
		if c.passwords.State() == secrets.InKeyring && c.passwords.File().Exists {
			once.Do(func() {
				c.appBus.Emit(bus.PasswordProblem{Text: i18n.Tr("Some passwords are still in an unencrypted file. You can move them to your keyring in Preferences.")})
			})
		}
	}
	bus.SubscribeCtx(c.appCtx, c.appBus, func(bus.PasswordStorageChanged) { check() })
	check()
}
