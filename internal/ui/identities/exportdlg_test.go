package identities

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"encoding/pem"
	"math/big"
	"testing"
	"time"

	"github.com/diamondburned/gotk4/pkg/gtk/v4"

	"github.com/one337/murmel/internal/certs"
	"github.com/one337/murmel/internal/i18n"
)

func openExport(t *testing.T, m *certs.Manager, meta certs.IdentityMeta, isRSA bool, saved chan []byte) *exportDialog {
	t.Helper()
	var d *exportDialog
	onMain(func() {
		d = newExportDialog(m, meta, isRSA, func(data []byte) { saved <- data })
		d.dialog.Present(gtk.NewWindow())
	})
	return d
}

// Save needs the same, non-empty password twice; the backup it makes
// opens with that password.
func TestGTKExportDialogPassword(t *testing.T) {
	needGTK(t)
	m := testManager(t)
	ident, err := m.Generate("Backup", "")
	if err != nil {
		t.Fatal(err)
	}
	saved := make(chan []byte, 1)
	d := openExport(t, m, ident.Meta, true, saved)
	onMain(func() {
		if d.button.Sensitive() {
			t.Error("Save possible without a password")
		}
		d.pw.SetText(testPassword)
		if d.button.Sensitive() {
			t.Error("Save possible without the repetition")
		}
		d.repeat.SetText("different")
		if d.button.Sensitive() || !d.repeat.HasCSSClass("error") {
			t.Error("different passwords not refused")
		}
		if got, want := d.status.Text(), i18n.Tr("The passwords do not match."); got != want {
			t.Errorf("status = %q", got)
		}
		d.repeat.SetText(testPassword)
		if !d.button.Sensitive() || d.repeat.HasCSSClass("error") {
			t.Error("matching passwords not accepted")
		}
		d.save()
	})
	var data []byte
	select {
	case data = <-saved:
	case <-time.After(10 * time.Second):
		t.Fatal("no backup made")
	}
	b, err := certs.ReadIdentity(data, testPassword)
	if err != nil {
		t.Fatal(err)
	}
	if b.Fingerprint() != certs.Fingerprint(ident.Certificate) {
		t.Error("backup holds another certificate")
	}
}

// A password a PKCS #12 file cannot hold is refused in the dialog.
func TestGTKExportDialogUnsupportedPassword(t *testing.T) {
	needGTK(t)
	m := testManager(t)
	ident, err := m.Generate("Emoji", "")
	if err != nil {
		t.Fatal(err)
	}
	saved := make(chan []byte, 1)
	d := openExport(t, m, ident.Meta, true, saved)
	onMain(func() {
		d.pw.SetText("geheim🔑")
		d.repeat.SetText("geheim🔑")
		d.save()
	})
	waitMain(t, "the backup attempt", func() bool { return !d.busy })
	onMain(func() {
		if !d.pw.HasCSSClass("error") || d.status.Text() != ExportError(certs.ErrPasswordCharacters) {
			t.Errorf("not refused: status %q", d.status.Text())
		}
	})
	select {
	case <-saved:
		t.Fatal("backup made with an unsupported password")
	default:
	}
}

// The hint about the official Mumble client shows for keys other than RSA
// only.
func TestGTKExportDialogNonRSAHint(t *testing.T) {
	needGTK(t)
	m := testManager(t)
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{SerialNumber: big.NewInt(1), NotBefore: time.Now(), NotAfter: time.Now().Add(time.Hour)}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	keyDER, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	ec, err := m.ImportPEM("EC", append(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}),
		pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: keyDER})...))
	if err != nil {
		t.Fatal(err)
	}
	hint := i18n.Tr("The official Mumble client cannot import this certificate: it accepts RSA keys only.")
	for _, isRSA := range []bool{false, true} {
		d := openExport(t, m, ec.Meta, isRSA, make(chan []byte, 1))
		onMain(func() {
			if shown := hasLabel(d.dialog, hint); shown == isRSA {
				t.Errorf("RSA %v: hint shown %v", isRSA, shown)
			}
		})
	}
}

// hasLabel reports whether a label with text is inside w.
func hasLabel(w gtk.Widgetter, text string) bool {
	base := gtk.BaseWidget(w)
	if l, ok := base.Cast().(*gtk.Label); ok && l.Label() == text {
		return true
	}
	for c := base.FirstChild(); c != nil; c = gtk.BaseWidget(c).NextSibling() {
		if hasLabel(c, text) {
			return true
		}
	}
	return false
}
