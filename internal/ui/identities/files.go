package identities

import (
	"context"
	"crypto/rsa"
	"errors"
	"log/slog"
	"os"

	"github.com/diamondburned/gotk4-adwaita/pkg/adw"
	"github.com/diamondburned/gotk4/pkg/gio/v2"
	"github.com/diamondburned/gotk4/pkg/glib/v2"
	"github.com/diamondburned/gotk4/pkg/gtk/v4"

	"github.com/one337/murmel/internal/certs"
	"github.com/one337/murmel/internal/i18n"
	"github.com/one337/murmel/internal/ui/gfileio"
)

// importFilters offers the files an identity can be imported from:
// PKCS #12 (the official Mumble client's backups) and PEM.
func importFilters() (*gio.ListStore, *gtk.FileFilter) {
	certificates := gtk.NewFileFilter()
	certificates.SetName(i18n.Tr("Certificates"))
	for _, suffix := range []string{"p12", "pfx", "pkcs12", "pem", "crt", "key"} {
		certificates.AddSuffix(suffix)
	}
	certificates.AddMIMEType("application/x-pkcs12")
	certificates.AddMIMEType("application/x-pem-file")
	all := gtk.NewFileFilter()
	all.SetName(i18n.Tr("All Files"))
	all.AddPattern("*")
	filters := gio.NewListStore(gtk.GTypeFileFilter)
	filters.Append(certificates.Object)
	filters.Append(all.Object)
	return filters, certificates
}

// Import lets the user pick a certificate file (PKCS #12, as the official
// Mumble client backs up, or PEM) and adds it to the identity store,
// asking for the file's password when it has one and for a name. owner is
// the widget that asked (its dialog or window); closing it abandons the
// import. done runs with the new identity, or with the stored one
// (existing true) when the certificate is already in the store. A file
// Murmel cannot read is reported over owner.
func Import(owner gtk.Widgetter, m *certs.Manager, done func(meta certs.IdentityMeta, existing bool)) {
	filters, certificates := importFilters()
	fd := gtk.NewFileDialog()
	fd.SetTitle(i18n.Tr("Import Certificate"))
	fd.SetModal(true)
	fd.SetFilters(filters)
	fd.SetDefaultFilter(certificates)
	fd.Open(context.Background(), gfileio.WindowOf(owner), func(res gio.AsyncResulter) {
		file, err := fd.OpenFinish(res)
		if err != nil || file == nil {
			return // cancelled
		}
		importFile(owner, m, file, done)
	})
}

// importFile reads file through GIO (it may lie on a network location),
// tries to open it without a password off the main loop, and continues in
// the import dialog.
func importFile(owner gtk.Widgetter, m *certs.Manager, file *gio.File, done func(certs.IdentityMeta, bool)) {
	ctx := gfileio.ForWidget(owner)
	name := gfileio.Name(file)
	gfileio.Load(ctx, file, gfileio.MaxPEMBytes, func(data []byte, err error) {
		if err != nil {
			slog.Info("identity file not read", "err", err)
			importFailed(owner, gfileio.Message(err))
			return
		}
		go func() {
			b, err := certs.ReadIdentity(data, "")
			glib.IdleAdd(func() {
				if ctx.Err() != nil {
					return
				}
				if err != nil && !errors.Is(err, certs.ErrWrongPassword) {
					slog.Info("identity file not imported", "err", err)
					importFailed(owner, ImportError(err))
					return
				}
				if err != nil {
					b = nil // protected: the dialog asks for the password
				} else if meta, ok := m.FindCertificate(b.Fingerprint()); ok {
					done(meta, true)
					return
				}
				d := newImportDialog(m, data, name, b, done)
				d.dialog.Present(owner)
				if b == nil {
					d.pwRow.GrabFocus()
				} else {
					d.nameRow.GrabFocus()
				}
			})
		}()
	})
}

// importFailed tells over owner why a file could not be imported.
func importFailed(owner gtk.Widgetter, msg string) {
	alert := adw.NewAlertDialog(i18n.Tr("Certificate Import Failed"), msg)
	alert.AddResponse("ok", i18n.Tr("OK"))
	alert.SetDefaultResponse("ok")
	alert.SetCloseResponse("ok")
	alert.Present(owner)
}

// backupFilters offers the backup's format: PKCS #12, which the official
// Mumble client imports.
func backupFilters() (*gio.ListStore, *gtk.FileFilter) {
	p12 := gtk.NewFileFilter()
	p12.SetName(i18n.Tr("PKCS #12 Certificates"))
	p12.AddSuffix("p12")
	p12.AddSuffix("pfx")
	p12.AddMIMEType("application/x-pkcs12")
	filters := gio.NewListStore(gtk.GTypeFileFilter)
	filters.Append(p12.Object)
	return filters, p12
}

// Export backs up the identity as a PKCS #12 file protected by a password
// the user chooses, readable only by the user where the location allows.
// owner is the widget that asked; closing it abandons the export. done
// runs on the main loop; exposed reports that Murmel could not make sure
// the file is private (see WarnExposedBackup).
func Export(owner gtk.Widgetter, m *certs.Manager, meta certs.IdentityMeta, done func(exposed bool, err error)) {
	ident, err := m.Load(meta.ID)
	if err != nil {
		done(false, err)
		return
	}
	_, isRSA := ident.PrivateKey.(*rsa.PrivateKey)
	d := newExportDialog(m, meta, isRSA, func(data []byte) { saveBackup(owner, meta, data, done) })
	d.dialog.Present(owner)
	d.pw.GrabFocus()
}

// saveBackup asks where to save the encrypted backup data and writes it.
func saveBackup(owner gtk.Widgetter, meta certs.IdentityMeta, data []byte, done func(exposed bool, err error)) {
	filters, p12 := backupFilters()
	fd := gtk.NewFileDialog()
	fd.SetTitle(i18n.Tr("Back Up Certificate"))
	fd.SetModal(true)
	fd.SetFilters(filters)
	fd.SetDefaultFilter(p12)
	fd.SetInitialName(meta.Name + ".p12")
	fd.Save(context.Background(), gfileio.WindowOf(owner), func(res gio.AsyncResulter) {
		file, err := fd.SaveFinish(res)
		if err != nil || file == nil {
			return // cancelled
		}
		ctx := gfileio.ForWidget(owner)
		path := file.Path()
		go func() {
			var mode os.FileMode
			var err error
			if path != "" {
				if err = writePrivateFile(path, data); err == nil {
					mode, err = fileMode(path)
				}
			}
			glib.IdleAdd(func() {
				if ctx.Err() != nil {
					return
				}
				if err != nil || path != "" {
					done(err == nil && exposed(true, mode), err)
					return
				}
				gfileio.SaveRemote(ctx, file, data, func(err error) { done(exposed(false, 0), err) })
			})
		}()
	})
}

// WarnExposedBackup tells over parent that a backup may be readable by
// others: shown when Murmel could not make sure it is private.
func WarnExposedBackup(parent gtk.Widgetter) {
	alert := adw.NewAlertDialog(i18n.Tr("Backup May Be Readable by Others"),
		i18n.Tr("The backup contains your private key. Murmel could not restrict access to the file at this location. Keep it where only you have access."))
	alert.AddResponse("ok", i18n.Tr("OK"))
	alert.SetDefaultResponse("ok")
	alert.SetCloseResponse("ok")
	alert.Present(parent)
}

// exposed reports whether a written backup may be readable by others:
// always without a local path (local false), else when its mode after
// writing still grants group or others any access (FAT, exFAT, some
// network file systems).
func exposed(local bool, mode os.FileMode) bool {
	return !local || mode.Perm()&0o077 != 0
}

// fileMode is the file's mode as the file system reports it after writing.
func fileMode(path string) (os.FileMode, error) {
	fi, err := os.Stat(path)
	if err != nil {
		return 0, err
	}
	return fi.Mode(), nil
}

// chmod sets the file mode; tests replace it.
var chmod = (*os.File).Chmod

// writePrivateFile writes data readable only by the user. The mode is set
// before anything is written, also when an existing file is overwritten
// (os.WriteFile applies its mode only to new files). No temporary file and
// rename: a path from the document portal allows only this one file. A
// file system without Unix permissions (FAT, exFAT) refuses the mode; the
// file is written all the same.
func writePrivateFile(path string, data []byte) error {
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600)
	if err != nil {
		return err
	}
	if err := chmod(f, 0o600); err != nil {
		slog.Info("export: file permissions not set", "err", err)
	}
	if _, err := f.Write(data); err != nil {
		_ = f.Close()
		return err
	}
	return f.Close()
}
