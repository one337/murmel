// Package weakref lets a signal closure reach the object it is attached to
// (or one of that object's ancestors) without holding it.
//
// A closure attached to a widget, or to one of its event controllers or
// descendants, must not hold that widget or an ancestor strongly: gotk4
// keeps the closure as long as the object lives, and the strong reference
// keeps the object alive in turn, so neither is ever freed. Such a closure
// captures a Ref instead (or only an ID it looks up when called). A struct
// that builds a dialog and holds its widgets (an owner struct) is reached
// through Owned instead. See CONTRIBUTING.md.
package weakref

import (
	"weak"

	coreglib "github.com/diamondburned/gotk4/pkg/core/glib"
	"github.com/diamondburned/gotk4/pkg/glib/v2"
)

// Ref is a weak reference to a GObject.
type Ref[T coreglib.Objector] struct {
	ref *coreglib.WeakRef[T]
}

// New returns a weak reference to obj.
func New[T coreglib.Objector](obj T) Ref[T] {
	return Ref[T]{ref: coreglib.NewWeakRef(obj)}
}

// Get returns the object, or ok false once it no longer exists.
func (r Ref[T]) Get() (obj T, ok bool) {
	obj = r.ref.Get()
	var zero T
	return obj, any(obj) != any(zero)
}

// Do calls f with the object if it still exists.
func (r Ref[T]) Do(f func(T)) {
	if obj, ok := r.Get(); ok {
		f(obj)
	}
}

// anchors keeps owner structs alive while the object they belong to
// exists. Main loop only.
var (
	anchors    = map[uint64]any{}
	nextAnchor uint64
)

// Owned keeps owner alive exactly as long as obj (usually the dialog it
// builds) and returns a weak pointer to it for the closures of obj's
// widgets: they call Value and do nothing when it returns nil. owner may
// hold the widgets inside obj, but obj itself only through New. Main loop
// only; the anchor is released on the main loop once obj is finalized.
func Owned[O any](obj coreglib.Objector, owner *O) weak.Pointer[O] {
	id := nextAnchor
	nextAnchor++
	anchors[id] = owner
	// The notify may run while GTK finalizes obj; it only queues the
	// release for the main loop.
	coreglib.WeakRefObject(obj, func() {
		glib.IdleAdd(func() bool {
			delete(anchors, id)
			return false
		})
	})
	return weak.Make(owner)
}
