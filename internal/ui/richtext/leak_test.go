package richtext

import (
	"bytes"
	"encoding/base64"
	"image"
	"image/color"
	"image/png"
	"os"
	"runtime"
	"sync/atomic"
	"testing"
	"time"

	"github.com/diamondburned/gotk4-adwaita/pkg/adw"
	coreglib "github.com/diamondburned/gotk4/pkg/core/glib"
	"github.com/diamondburned/gotk4/pkg/glib/v2"
	"github.com/diamondburned/gotk4/pkg/gtk/v4"
)

// The leak tests need GTK and a display. tools/verify.sh runs them under
// xvfb-run with MURMEL_GTK_TESTS=1 in a throwaway HOME; without it they
// are skipped, so a plain `go test` never opens a display.

var gtkReady bool

// GTK must run on the main thread: TestMain keeps it there and runs the
// GLib main loop while the tests run on other goroutines.
func init() { runtime.LockOSThread() }

func TestMain(m *testing.M) {
	if os.Getenv("MURMEL_GTK_TESTS") == "" || !gtk.InitCheck() {
		os.Exit(m.Run())
	}
	adw.Init()
	gtkReady = true
	loop := glib.NewMainLoop(nil, false)
	code := 0
	go func() {
		code = m.Run()
		glib.IdleAdd(func() bool { loop.Quit(); return false })
	}()
	loop.Run()
	os.Exit(code)
}

// needGTK skips a test without GTK and a display, except in CI, where a
// GTK test that cannot run is a failure.
func needGTK(t *testing.T) {
	t.Helper()
	if gtkReady {
		return
	}
	if os.Getenv("CI") != "" {
		t.Fatal("GTK test cannot run in CI: GTK did not start (tools/verify.sh runs it under xvfb-run with MURMEL_GTK_TESTS=1)")
	}
	t.Skip("needs GTK and a display: run with MURMEL_GTK_TESTS=1 under xvfb-run (tools/verify.sh)")
}

func onMain(f func()) {
	done := make(chan struct{})
	glib.IdleAdd(func() bool { f(); close(done); return false })
	<-done
}

func pngDataURI(t *testing.T) string {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, 48, 32))
	for x := range 48 {
		for y := range 32 {
			img.Set(x, y, color.RGBA{200, 120, 40, 255})
		}
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatal(err)
	}
	return "data:image/png;base64," + base64.StdEncoding.EncodeToString(buf.Bytes())
}

// An image in rich text (as in a chat row) is freed with the row: its
// click gesture must not keep the picture or its texture alive.
func TestGTKImagesAreFreedWithTheirRow(t *testing.T) {
	needGTK(t)
	html := `Bild<br/><img src="` + pngDataURI(t) + `"/>`
	var win *gtk.Window
	var list *gtk.Box
	onMain(func() {
		win = gtk.NewWindow()
		list = gtk.NewBox(gtk.OrientationVertical, 0)
		win.SetChild(list)
		win.Present()
	})
	t.Cleanup(func() { onMain(func() { win.Destroy() }) })

	const rows = 50
	var watched int
	var finalized atomic.Int32
	for range rows {
		onMain(func() {
			row := Build(Parse(html), Options{MaxImageWidth: 360, MaxImageHeight: 360})
			var walk func(w gtk.Widgetter)
			walk = func(w gtk.Widgetter) {
				if p, ok := coreglib.BaseObject(w).Cast().(*gtk.Picture); ok {
					watched++
					coreglib.WeakRefObject(p, func() { finalized.Add(1) })
					if tex := p.Paintable(); tex != nil {
						watched++
						coreglib.WeakRefObject(tex, func() { finalized.Add(1) })
					}
				}
				for c := gtk.BaseWidget(w).FirstChild(); c != nil; c = gtk.BaseWidget(c).NextSibling() {
					walk(c)
				}
			}
			walk(row)
			list.Append(row)
		})
		// The row scrolls out of the history.
		onMain(func() { list.Remove(list.FirstChild()) })
	}
	if watched != 2*rows {
		t.Fatalf("watched %d pictures and textures, want %d", watched, 2*rows)
	}
	const tolerance = 2
	alive := 0
	for range 3 {
		for range 12 {
			runtime.GC()
			time.Sleep(20 * time.Millisecond)
			onMain(func() {})
		}
		alive = watched - int(finalized.Load())
		if alive <= tolerance {
			return
		}
	}
	t.Fatalf("%d of %d pictures and textures still alive after their rows were removed (tolerance %d)", alive, watched, tolerance)
}

// Clicking an image still opens it after garbage collection: the click
// gesture holds the picture only weakly, the window holds it for real.
func TestGTKImageClickOpensViewAfterGC(t *testing.T) {
	needGTK(t)
	html := `<img src="` + pngDataURI(t) + `"/>`
	var win *adw.Window
	onMain(func() {
		win = adw.NewWindow()
		win.SetDefaultSize(600, 400)
		win.SetContent(Build(Parse(html), Options{MaxImageWidth: 360, MaxImageHeight: 360}))
		win.Present()
	})
	t.Cleanup(func() { onMain(func() { win.Destroy() }) })
	for range 12 {
		runtime.GC()
		time.Sleep(20 * time.Millisecond)
		onMain(func() {})
	}
	opened := false
	onMain(func() {
		var click *gtk.GestureClick
		var walk func(w gtk.Widgetter)
		walk = func(w gtk.Widgetter) {
			if _, ok := coreglib.BaseObject(w).Cast().(*gtk.Picture); ok {
				ctrls := gtk.BaseWidget(w).ObserveControllers()
				for i := uint(0); i < ctrls.NItems(); i++ {
					if g, ok := ctrls.Item(i).Cast().(*gtk.GestureClick); ok {
						click = g
					}
				}
			}
			for c := gtk.BaseWidget(w).FirstChild(); c != nil && click == nil; c = gtk.BaseWidget(c).NextSibling() {
				walk(c)
			}
		}
		walk(win.Content())
		if click == nil {
			t.Error("no click gesture on the picture")
			return
		}
		coreglib.BaseObject(click).Emit("released", 1, 0.0, 0.0)
		if d := win.VisibleDialog(); d != nil {
			opened = true
			d.ForceClose()
		}
	})
	if !opened {
		t.Fatal("the click did not open the image after GC")
	}
}
