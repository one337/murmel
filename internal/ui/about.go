package ui

import (
	"github.com/diamondburned/gotk4-adwaita/pkg/adw"
	"github.com/diamondburned/gotk4/pkg/gtk/v4"

	"github.com/one337/murmel/internal/i18n"
	"github.com/one337/murmel/internal/settings"
)

// Version is the release version, set at build time:
//
//	go build -ldflags "-X github.com/one337/murmel/internal/ui.Version=$(cat VERSION)"
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
	addLegalSections(d)
	var parent gtk.Widgetter
	if a.window != nil {
		parent = a.window
	}
	d.Present(parent)
}

// addLegalSections lists the components built into Murmel (see
// tools/licenses.txt; their full license texts are installed with it).
// Copyright lines are those of the components' license files; the free
// text only shows for custom licenses.
func addLegalSections(d *adw.AboutDialog) {
	for _, c := range []struct {
		name, copyright string
		license         gtk.License
		text            string
	}{
		{"gotk4, gotk4-adwaita", "", gtk.LicenseMPL20, ""},
		{i18n.Tr("gumble (Murmel's fork)"), "", gtk.LicenseMPL20, ""},
		{i18n.Tr("Mumble protocol and voice encryption"), "© 2005–2013 Thorvald Natvig and others", gtk.LicenseBSD3, ""},
		{"malgo, miniaudio", "", gtk.LicenseCustom, i18n.Tr("Unlicense (public domain).")},
		{"protobuf-go", "© 2018 The Go Authors", gtk.LicenseBSD3, ""},
		{"go-pkcs12", "© 2015, 2018, 2019 Opsmate, Inc.", gtk.LicenseBSD3, ""},
		{"golang.org/x/crypto", "© 2009 The Go Authors", gtk.LicenseBSD3, ""},
		{"golang.org/x/sync", "© 2009 The Go Authors", gtk.LicenseBSD3, ""},
		{"golang.org/x/image", "© 2009 The Go Authors", gtk.LicenseBSD3, ""},
		{"golang.org/x/net", "© 2009 The Go Authors", gtk.LicenseBSD3, ""},
		{"golang.org/x/text", "© 2009 The Go Authors", gtk.LicenseBSD3, ""},
		{"hraban/opus", "© 2015–2022 Go Opus Authors", gtk.LicenseMITX11, ""},
		{"oggvorbis, vorbis", "© 2016 Johann Freymuth", gtk.LicenseMITX11, ""},
		{"gotext", "© 2016 Leonel Quinteros", gtk.LicenseMITX11, ""},
		{"GNOME Icon Development Kit", "", gtk.LicenseCustom, i18n.Tr("CC0 1.0 (public domain), for two user icons.")},
	} {
		d.AddLegalSection(c.name, c.copyright, c.license, c.text)
	}
}
