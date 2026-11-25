package gfileio

import (
	"bytes"
	"context"
	"errors"
	"image"
	"image/jpeg"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/diamondburned/gotk4/pkg/gio/v2"
	"github.com/diamondburned/gotk4/pkg/glib/v2"

	"github.com/one337/murmel/internal/ui/resources"
)

// withMainLoop runs a GLib main loop for the test, so done callbacks run.
func withMainLoop(t *testing.T) {
	t.Helper()
	loop := glib.NewMainLoop(nil, false)
	go loop.Run()
	t.Cleanup(loop.Quit)
}

type result struct {
	data []byte
	err  error
}

func load(t *testing.T, ctx context.Context, file *gio.File, max int64) (result, bool) {
	t.Helper()
	got := make(chan result, 1)
	Load(ctx, file, max, func(data []byte, err error) { got <- result{data, err} })
	select {
	case r := <-got:
		return r, true
	case <-time.After(3 * time.Second):
		return result{}, false
	}
}

func TestLoadLocalFile(t *testing.T) {
	withMainLoop(t)
	path := filepath.Join(t.TempDir(), "id.pem")
	want := []byte("-----BEGIN CERTIFICATE-----\n")
	if err := os.WriteFile(path, want, 0o600); err != nil {
		t.Fatal(err)
	}
	r, ok := load(t, context.Background(), gio.NewFileForPath(path), 1024)
	if !ok || r.err != nil || !bytes.Equal(r.data, want) {
		t.Fatalf("Load = %q, %v (done: %v)", r.data, r.err, ok)
	}
}

// A file without a local path (here a GResource) is read through GIO.
func TestLoadWithoutLocalPath(t *testing.T) {
	withMainLoop(t)
	resources.Register()
	file := gio.NewFileForURI("resource:///io/github/one337/murmel/style.css")
	if file.Path() != "" {
		t.Fatalf("resource has a local path %q", file.Path())
	}
	want, err := os.ReadFile("../../../data/style.css")
	if err != nil {
		t.Fatal(err)
	}
	r, ok := load(t, context.Background(), file, 1<<20)
	if !ok || r.err != nil || !bytes.Equal(r.data, want) {
		t.Fatalf("Load: %d bytes, %v (done: %v)", len(r.data), r.err, ok)
	}
}

func TestLoadRefusesTooLargeFile(t *testing.T) {
	withMainLoop(t)
	path := filepath.Join(t.TempDir(), "big")
	if err := os.WriteFile(path, make([]byte, 2048), 0o600); err != nil {
		t.Fatal(err)
	}
	r, ok := load(t, context.Background(), gio.NewFileForPath(path), 1024)
	if !ok || !errors.Is(r.err, ErrTooLarge) {
		t.Fatalf("Load err = %v (done: %v), want ErrTooLarge", r.err, ok)
	}
	if Message(r.err) == r.err.Error() {
		t.Error("Message does not describe ErrTooLarge for the user")
	}
}

// The limit holds while reading too, whatever size was reported before.
func TestReadLimitedStopsAfterLimit(t *testing.T) {
	const max = 100_000
	in := gio.NewMemoryInputStreamFromBytes(glib.NewBytes(make([]byte, 3*max)))
	_, err := readLimited(context.Background(), &in.InputStream, max)
	if !errors.Is(err, ErrTooLarge) {
		t.Fatalf("err = %v, want ErrTooLarge", err)
	}
	// It stopped early: what is left of the stream was never read.
	rest, _ := in.Skip(context.Background(), 3*max)
	if rest < 3*max-max-readChunk {
		t.Fatalf("read %d bytes, want at most %d", 3*max-rest, max+readChunk)
	}

	exact := gio.NewMemoryInputStreamFromBytes(glib.NewBytes(make([]byte, max)))
	if data, err := readLimited(context.Background(), &exact.InputStream, max); err != nil || len(data) != max {
		t.Fatalf("exactly the limit: %d bytes, %v", len(data), err)
	}
}

func TestLoadAfterCancelDoesNotCallDone(t *testing.T) {
	withMainLoop(t)
	path := filepath.Join(t.TempDir(), "f")
	if err := os.WriteFile(path, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, ok := load(t, ctx, gio.NewFileForPath(path), 1024); ok {
		t.Fatal("done ran after the context ended")
	}
}

func TestSaveRemoteWritesPrivateFile(t *testing.T) {
	withMainLoop(t)
	path := filepath.Join(t.TempDir(), "backup.pem")
	got := make(chan error, 1)
	SaveRemote(context.Background(), gio.NewFileForPath(path), []byte("key"), func(err error) { got <- err })
	select {
	case err := <-got:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("done did not run")
	}
	fi, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if mode := fi.Mode().Perm(); mode != 0o600 {
		t.Errorf("mode = %o, want 600", mode)
	}
	if data, _ := os.ReadFile(path); string(data) != "key" {
		t.Errorf("content = %q", data)
	}
}

func TestName(t *testing.T) {
	for in, want := range map[string]string{
		"/x/simon.pem": "simon",
		"/x/a.b.pem":   "a.b",
		"/x/noext":     "noext",
		"/x/.hidden":   ".hidden",
	} {
		if got := Name(gio.NewFileForPath(in)); got != want {
			t.Errorf("Name(%q) = %q, want %q", in, got, want)
		}
	}
}

// The orientation stored in a JPEG reaches the pixbuf, so callers can
// apply it.
func TestPixbufKeepsOrientation(t *testing.T) {
	pb, err := Pixbuf(orientedJPEG(t, 6))
	if err != nil {
		t.Fatal(err)
	}
	if got := pb.Option("orientation"); got != "6" {
		t.Fatalf("orientation option = %q, want 6", got)
	}
	if r := pb.ApplyEmbeddedOrientation(); r == nil || r.Width() != pb.Height() {
		t.Fatal("orientation not applicable")
	}
}

// orientedJPEG is a 40×20 JPEG whose EXIF data sets orientation.
func orientedJPEG(t *testing.T, orientation byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, image.NewGray(image.Rect(0, 0, 40, 20)), nil); err != nil {
		t.Fatal(err)
	}
	tiff := []byte{
		'M', 'M', 0x00, 0x2A, 0, 0, 0, 8, // big-endian TIFF, first IFD at 8
		0x00, 0x01, // one entry
		0x01, 0x12, 0x00, 0x03, 0, 0, 0, 1, 0x00, orientation, 0, 0, // Orientation, SHORT
		0, 0, 0, 0, // no next IFD
	}
	payload := append([]byte("Exif\x00\x00"), tiff...)
	app1 := append([]byte{0xFF, 0xE1, byte((len(payload) + 2) >> 8), byte(len(payload) + 2)}, payload...)
	jpg := buf.Bytes()
	return append(append(append([]byte{}, jpg[:2]...), app1...), jpg[2:]...)
}
