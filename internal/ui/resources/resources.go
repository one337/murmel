// Package resources embeds the compiled GResource bundle (app icon) and
// registers it with GIO so the icon resolves even when running from the
// source tree without an installed hicolor theme.
//
// Regenerate murmel.gresource after changing data/icons:
//
//go:generate glib-compile-resources --sourcedir=../../../data --target=murmel.gresource ../../../data/io.github.murmel.Murmel.gresource.xml
package resources

import (
	_ "embed"
	"log/slog"

	"github.com/diamondburned/gotk4/pkg/gio/v2"
	"github.com/diamondburned/gotk4/pkg/glib/v2"
)

//go:embed murmel.gresource
var bundle []byte

// Register loads the embedded bundle into the global GResource namespace.
// GtkApplication adds "<resource-base-path>/icons" to the icon theme, so the
// app icon under /io/github/murmel/Murmel/icons is picked up automatically.
// Call once before the application starts.
func Register() {
	res, err := gio.NewResourceFromData(glib.NewBytesWithGo(bundle))
	if err != nil {
		slog.Warn("resources: loading bundle failed", "err", err)
		return
	}
	gio.ResourcesRegister(res)
}
