package bus

import "sync"

// The bus runs subscriber handlers on "the main loop", but it does not
// import GLib itself: the audio and protocol layers use the bus and must
// stay free of gotk4. The UI installs glib.IdleAdd with SetMainLoop (package
// ui does so in an init function, so every program that shows a window has
// it before the first event). Without it — headless tools and tests — a
// single dispatcher goroutine stands in for the main loop.

var (
	mainLoopMu sync.RWMutex
	mainLoop   func(f func() bool)

	headlessOnce  sync.Once
	headlessQueue chan func() bool
)

// SetMainLoop installs the function that schedules a callback on the main
// loop with glib.IdleAdd semantics: f runs on the main loop and is called
// again while it returns true. Buses created before the call use it too.
func SetMainLoop(idleAdd func(f func() bool)) {
	mainLoopMu.Lock()
	mainLoop = idleAdd
	mainLoopMu.Unlock()
}

// dispatchMainLoop is the production idleAdd hook of every Bus.
func dispatchMainLoop(f func() bool) {
	mainLoopMu.RLock()
	idleAdd := mainLoop
	mainLoopMu.RUnlock()
	if idleAdd != nil {
		idleAdd(f)
		return
	}
	headlessOnce.Do(func() {
		headlessQueue = make(chan func() bool, bufferSize)
		go func() {
			for f := range headlessQueue {
				for f() {
				}
			}
		}()
	})
	headlessQueue <- f
}
