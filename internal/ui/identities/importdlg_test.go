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

const testPassword = "Murmel-Test-ä1"

type importResult struct {
	meta     certs.IdentityMeta
	existing bool
}

func receive(t *testing.T, results chan importResult) importResult {
	t.Helper()
	select {
	case r := <-results:
		return r
	case <-time.After(10 * time.Second):
		t.Fatal("the dialog did not finish")
	}
	return importResult{}
}

// backup returns a password-protected backup of a new identity named cn.
func backup(t *testing.T, cn string) []byte {
	t.Helper()
	src := testManager(t)
	ident, err := src.Generate(cn, "")
	if err != nil {
		t.Fatal(err)
	}
	data, err := src.ExportP12(ident.Meta.ID, testPassword)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func openDialog(t *testing.T, m *certs.Manager, data []byte, b *certs.Bundle, results chan importResult) *importDialog {
	t.Helper()
	var d *importDialog
	onMain(func() {
		d = newImportDialog(m, data, "backup", b, func(meta certs.IdentityMeta, existing bool) {
			results <- importResult{meta, existing}
		})
		d.dialog.Present(gtk.NewWindow())
	})
	return d
}

// A protected file asks for its password; a wrong one is marked and
// said, the right one leads to the name, prefilled with the certificate's
// CN, and Import stores the identity.
func TestGTKImportDialogPassword(t *testing.T) {
	needGTK(t)
	m := testManager(t)
	results := make(chan importResult, 1)
	d := openDialog(t, m, backup(t, "Jürgen Ä"), nil, results)

	onMain(func() {
		if d.details.Visible() || !d.intro.Visible() {
			t.Error("the password is not asked first")
		}
		d.pwRow.SetText("wrong")
		d.activate()
	})
	waitMain(t, "the wrong password to be checked", func() bool { return !d.busy })
	onMain(func() {
		if !d.pwRow.HasCSSClass("error") {
			t.Error("wrong password not marked")
		}
		if got, want := d.status.Text(), i18n.Tr("Wrong password. If it is correct, the file may be damaged."); got != want {
			t.Errorf("status = %q", got)
		}
		if d.bundle != nil {
			t.Error("opened with a wrong password")
		}
		d.pwRow.SetText(testPassword)
		if d.pwRow.HasCSSClass("error") {
			t.Error("error mark stays while the password is edited")
		}
		d.activate()
	})
	waitMain(t, "the file to open", func() bool { return d.bundle != nil })
	onMain(func() {
		if d.intro.Visible() || !d.details.Visible() {
			t.Error("the name is not shown after unlocking")
		}
		if d.nameRow.Text() != "Jürgen Ä" {
			t.Errorf("name = %q, want the CN", d.nameRow.Text())
		}
		d.nameRow.SetText("  ")
		if d.button.Sensitive() {
			t.Error("Import possible without a name")
		}
		d.nameRow.SetText("Backup from Mumble")
		d.activate()
	})
	r := receive(t, results)
	if r.existing || r.meta.Name != "Backup from Mumble" {
		t.Fatalf("result %+v", r)
	}
	if list, _ := m.List(); len(list) != 1 {
		t.Fatalf("%d identities stored", len(list))
	}
}

// An unprotected file goes straight to the name; a certificate that is
// already stored is not added again.
func TestGTKImportDialogDuplicate(t *testing.T) {
	needGTK(t)
	m := testManager(t)
	b, err := certs.ReadIdentity(backup(t, "Twice"), testPassword)
	if err != nil {
		t.Fatal(err)
	}
	results := make(chan importResult, 2)
	d := openDialog(t, m, nil, b, results)
	onMain(func() {
		if d.intro.Visible() || d.nameRow.Text() != "Twice" {
			t.Errorf("unprotected file: password shown %v, name %q", d.intro.Visible(), d.nameRow.Text())
		}
		d.activate()
	})
	first := receive(t, results)

	d = openDialog(t, m, nil, b, results)
	onMain(func() { d.activate() })
	second := receive(t, results)
	if !second.existing || second.meta.ID != first.meta.ID {
		t.Fatalf("second import: %+v, first %+v", second, first)
	}
	if list, _ := m.List(); len(list) != 1 {
		t.Fatalf("%d identities stored", len(list))
	}
}

// Without a CN the file name is the suggested name.
func TestGTKImportDialogNameFallback(t *testing.T) {
	needGTK(t)
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
	b, err := certs.ReadIdentity(append(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}),
		pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: keyDER})...), "")
	if err != nil {
		t.Fatal(err)
	}
	d := openDialog(t, testManager(t), nil, b, make(chan importResult, 1))
	onMain(func() {
		if d.nameRow.Text() != "backup" {
			t.Errorf("name = %q, want the file name", d.nameRow.Text())
		}
	})
}
