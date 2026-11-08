package ui

import (
	"github.com/diamondburned/gotk4-adwaita/pkg/adw"
	"github.com/diamondburned/gotk4/pkg/gtk/v4"

	"io.github/murmel/internal/settings"
)

// Version is the release version, set at build time:
//
//	go build -ldflags "-X io.github/murmel/internal/ui.Version=$(cat VERSION)"
var Version = "dev"

const (
	websiteURL = "https://simon-tschoep.de/software/murmel/"
	issueURL   = "https://github.com/one337/murmel/issues"
)

// showAbout presents the About dialog.
func (a *App) showAbout() {
	d := adw.NewAboutDialog()
	d.SetApplicationName("Murmel")
	d.SetApplicationIcon(settings.SchemaID)
	d.SetVersion(Version)
	d.SetDeveloperName("Simon Tschöp")
	d.SetDevelopers([]string{"Simon Tschöp"})
	d.SetCopyright("© 2026 Simon Tschöp")
	d.SetLicenseType(gtk.LicenseGPL30)
	d.SetWebsite(websiteURL)
	d.SetIssueURL(issueURL)
	var parent gtk.Widgetter
	if a.window != nil {
		parent = a.window
	}
	d.Present(parent)
}
