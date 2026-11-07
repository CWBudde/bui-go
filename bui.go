// SPDX-License-Identifier: GPL-2.0-or-later

// Package bui is a Go binding to BUI's C kit, which provides Blender's UI widgets.
//
// Engines: on Linux amd64 with cgo it calls the kit natively. Everywhere else, and with
// the build tag bui_nocgo, it runs the kit as an embedded WebAssembly module with no cgo:
// with a real window on Linux through X11, and headless on other systems, where NewWindow
// and Run report ErrNoDisplay. The API and the pixels are the same. A binary with the
// wasm engine is distributed under GPLv3. The README has the limits of each engine.
//
// Threading: Run must be called from the main goroutine, and all kit-touching
// API is UI-thread only, meaning the main goroutine before and after Run, and
// handlers and Do functions during it. The kit keeps unlocked global state, so
// its calls must be serialized. Other goroutines reach the UI thread through
// Do, the only function safe to call from anywhere.
package bui

import (
	"errors"
	"runtime"
	"sync"
)

// init runs on the main goroutine; this keeps it on the main OS thread.
func init() {
	runtime.LockOSThread()
}

var (
	doMu    sync.Mutex
	doQueue []func()
)

// Do queues f to run on the UI thread, in FIFO order. It is safe from any
// goroutine and never blocks. Functions queued before Run run when Run starts.
func Do(f func()) {
	doMu.Lock()
	defer doMu.Unlock()

	doQueue = append(doQueue, f)

	host.Wake()
}

func pendingDo() int {
	doMu.Lock()
	defer doMu.Unlock()

	return len(doQueue)
}

// drainDo runs what is queued right now; functions they queue wait for the next
// drain. If one panics, those behind it go back to the front of the queue.
func drainDo() {
	doMu.Lock()
	queue := doQueue
	doQueue = nil
	doMu.Unlock()

	next := 0
	defer func() {
		if next < len(queue) {
			doMu.Lock()

			doQueue = append(queue[next:], doQueue...)
			doMu.Unlock()
		}
	}()

	for next < len(queue) {
		f := queue[next]
		next++

		f()
	}
}

// EvalNumber evaluates a Blender-style number expression such as "2*pi". The
// second result is false when the expression is invalid.
func EvalNumber(expr string) (float64, bool) {
	return kit.EvalNumber(expr)
}

// ErrNoDisplay is returned when the kit cannot open a window system.
var ErrNoDisplay = errors.New("bui: no display available")

// Supported reports whether windows can be opened. It opens the display. It is
// UI-thread only.
func Supported() bool {
	return host.Supported()
}

// UI-thread state of the loop.
var inRun, inLoop, quitRequested bool

// Run runs the message loop until Quit or until every window is closed. It must
// be called from the main goroutine. It returns ErrNoDisplay without a window
// system, leaving queued Do functions in place.
//
// A panic in a handler, or in a Do function running inside the loop, ends the
// process, as it would abandon C frames. A Do function that Run itself drains
// before or after the loop runs on the Go stack instead, so its panic propagates
// from Run; the functions behind it stay queued and Run can be called again.
func Run() error {
	if inRun || emitDepth > 0 {
		panic("bui: Run called from inside Run or a handler")
	}

	if !Supported() {
		return ErrNoDisplay
	}

	inRun = true
	defer func() { inRun, inLoop = false, false }()
	// Wakes sent before this were lost, so what they announced runs right here.
	doMu.Lock()
	host.WakeInit(handleWake)
	doMu.Unlock()
	runDeferred()
	drainDo()

	if !quitRequested {
		inLoop = true

		host.Run()

		inLoop = false
	}
	// The loop can end with work pending, such as a handler that closed a node
	// and then quit. Run this before the caller tears down; a function queued from
	// here on waits for the next Run.
	runDeferred()
	drainDo()

	quitRequested = false

	return nil
}

// Quit ends the message loop. Outside the loop it makes the next Run return
// without looping: BK_window_run erases an earlier BK_window_quit, which would
// otherwise lose a Do(Quit) queued before Run. It is UI-thread only; from other
// goroutines use Do(Quit).
func Quit() {
	if inLoop {
		host.Quit()
		return
	}

	quitRequested = true
}

// handleWake is the loop's wake callback. It runs inside the loop but outside any
// kit call, so kit calls and destruction are allowed. Queued destruction goes
// first: it was requested before anything these functions will do, and they flush
// their own, as every binding call that can emit does.
func handleWake() {
	runDeferred()
	drainDo()
}
