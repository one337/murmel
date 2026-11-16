// Package weakref lets a signal closure reach the object it is attached to
// (or one of that object's ancestors) without holding it.
//
// A closure attached to a widget, or to one of its event controllers or
// descendants, must not hold that widget or an ancestor strongly: gotk4
// keeps the closure as long as the object lives, and the strong reference
// keeps the object alive in turn, so neither is ever freed. Such a closure
// captures a Ref instead (or only an ID it looks up when called). See
// CONTRIBUTING.md.
package weakref

import (
	coreglib "github.com/diamondburned/gotk4/pkg/core/glib"
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
