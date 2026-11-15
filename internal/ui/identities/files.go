package identities

import (
	"context"
	"os"
	"path/filepath"
	"strings"

	"github.com/diamondburned/gotk4/pkg/gio/v2"
	"github.com/diamondburned/gotk4/pkg/gtk/v4"

	"io.github/murmel/internal/certs"
)

func pemFilters() (*gio.ListStore, *gtk.FileFilter) {
	pem := gtk.NewFileFilter()
	pem.SetName(tr("PEM Certificates"))
	pem.AddSuffix("pem")
	pem.AddSuffix("crt")
	pem.AddSuffix("key")
	pem.AddMIMEType("application/x-pem-file")
	all := gtk.NewFileFilter()
	all.SetName(tr("All Files"))
	all.AddPattern("*")
	filters := gio.NewListStore(gtk.GTypeFileFilter)
	filters.Append(pem.Object)
	filters.Append(all.Object)
	return filters, pem
}

// Import lets the user pick a PEM file (certificate and private key) and
// adds it to the identity store.
func Import(parent *gtk.Window, m *certs.Manager, done func(certs.IdentityMeta), failed func(error)) {
	filters, pem := pemFilters()
	fd := gtk.NewFileDialog()
	fd.SetTitle(tr("Import Identity"))
	fd.SetModal(true)
	fd.SetFilters(filters)
	fd.SetDefaultFilter(pem)
	fd.Open(context.Background(), parent, func(res gio.AsyncResulter) {
		file, err := fd.OpenFinish(res)
		if err != nil || file == nil {
			return // cancelled
		}
		path := file.Path()
		name := strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))
		ident, err := m.ImportFiles(name, path, "")
		if err != nil {
			failed(err)
			return
		}
		done(ident.Meta)
	})
}

// Export saves the identity's certificate and private key as one PEM
// file, readable only by the user.
func Export(parent *gtk.Window, m *certs.Manager, meta certs.IdentityMeta, done func(error)) {
	filters, pem := pemFilters()
	fd := gtk.NewFileDialog()
	fd.SetTitle(tr("Export Identity"))
	fd.SetModal(true)
	fd.SetFilters(filters)
	fd.SetDefaultFilter(pem)
	fd.SetInitialName(meta.Name + ".pem")
	fd.Save(context.Background(), parent, func(res gio.AsyncResulter) {
		file, err := fd.SaveFinish(res)
		if err != nil || file == nil {
			return // cancelled
		}
		data, err := m.ExportPEM(meta.ID)
		if err == nil {
			err = writePrivateFile(file.Path(), data)
		}
		done(err)
	})
}

// writePrivateFile writes data readable only by the user. The mode is set
// before anything is written, also when an existing file is overwritten
// (os.WriteFile applies its mode only to new files). No temporary file and
// rename: a path from the document portal allows only this one file.
func writePrivateFile(path string, data []byte) error {
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600)
	if err != nil {
		return err
	}
	if err := f.Chmod(0o600); err != nil {
		f.Close()
		return err
	}
	if _, err := f.Write(data); err != nil {
		f.Close()
		return err
	}
	return f.Close()
}
