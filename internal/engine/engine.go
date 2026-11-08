// SPDX-License-Identifier: GPL-2.0-or-later

// Package engine is the seam between package bui and what runs BUI's C kit. It
// holds interfaces only, in Go types, so that package bui needs no cgo and an
// engine can be native code or a wasm module.
//
// Threading: Kit and Host are UI-thread only, as the kit keeps unlocked global
// state. The one exception is Host.Wake.
package engine

import (
	"errors"
	"fmt"
	"os"
	"runtime/debug"
)

// ErrDisplayLost is Host.Run's error when the connection to the window system breaks.
var ErrDisplayLost = errors.New("bui: the connection to the display was lost")

// Opaque references to kit objects, meaningful only to the engine that made them:
// a C pointer natively, a linear-memory offset in a module. Zero is the null
// reference. They are not node identities, which are the ids of Kit.NodeSetID.
type (
	Node    uintptr
	Context uintptr
	Raster  uintptr
	Window  uintptr
)

// Event kinds of an EmitFunc, numbered as the kit's BK_EV_* values. Other kinds
// can arrive too, and receivers ignore what they do not know.
const (
	EvChange = 1
	EvClick  = 2
)

// Modifier bits of input calls, as the kit's BK_SHIFT and so on.
const (
	ModShift   = 1
	ModCtrl    = 2
	ModAlt     = 4
	ModButton1 = 8
)

// Rect is a rectangle in raster pixels.
type Rect struct{ X, Y, W, H int }

// WindowHost is what the kit asks of the window showing a context: BKHost without
// emit, which stays with Kit.SetEmit. Its methods run on the UI thread inside a Kit
// call, so they must not free anything the kit is using.
type WindowHost interface {
	// Request asks for a render on the next loop turn.
	Request()
	// SetCursor sets the pointer shape, one of the kit's BK_CURSOR_* values.
	SetCursor(cursor int)
	// Warp moves the pointer to window coordinates.
	Warp(x, y int)
	// ClipboardGet returns the clipboard's text. ok is false if there is none.
	ClipboardGet() (text string, ok bool)
	ClipboardSet(text string)
	// Schedule asks for Kit.Timer(ctx, timer) after ms milliseconds.
	Schedule(ms, timer int)
}

// EmitFunc receives a kit event for a node with a non-zero id. The engine filters
// out events without a node or for kit-internal nodes. It runs on the UI thread,
// inside the Kit call that caused the event.
type EmitFunc func(id uint32, kind int, a float64)

// Kit is the kit's retained tree, rendering and input. It mirrors the C API one
// call at a time, which is the whole point of the seam, so it stays one interface.
//
//nolint:interfacebloat // see above
type Kit interface {
	// SetEmit sets the receiver of events of contexts with the emit host.
	SetEmit(f EmitFunc)

	// NodeNew creates a detached node. It returns 0 for an unknown type.
	NodeNew(typ string) Node
	NodeFree(n Node)
	// NodeSetID stores id in the node, where 0 means "no id". Kit-internal nodes
	// have none.
	NodeSetID(n Node, id uint32)
	NodeID(n Node) uint32
	NodeParent(n Node) Node
	// NodeAdd appends child to parent, moving it from any parent it has. It
	// returns false if the kit refuses.
	NodeAdd(parent, child Node) bool
	NodeChildCount(n Node) int
	NodeChild(n Node, i int) Node
	// NodeRect returns the node's rectangle in raster pixels.
	NodeRect(n Node) (x, y, w, h int)
	// NodeRelayout measures n and the nodes in its context again and lays them out on
	// the next render. Sizes measured before a SetScale stay until it runs.
	NodeRelayout(n Node)

	// SetNum and SetStr return false for a key the kit does not know.
	SetNum(n Node, key string, v float64) bool
	SetStr(n Node, key, v string) bool
	// SetStrs sets a string list, such as a dropdown's "items". It returns false for
	// a key the kit does not know.
	SetStrs(n Node, key string, vs []string) bool
	// GetNum returns 0 and GetStr returns "" for an unknown key.
	GetNum(n Node, key string) float64
	GetStr(n Node, key string) string

	// GeomSetPack selects the pack manager for n.
	GeomSetPack(n Node)
	GeomSet(n Node, key string, v float64)

	ContextNew(root Node) Context
	// ContextRoot returns the root the context was made with.
	ContextRoot(ctx Context) Node
	// ContextSetEmitHost installs the host that forwards events to SetEmit's
	// receiver. It must run before Host.WindowNew, which saves the host it finds.
	ContextSetEmitHost(ctx Context)
	// ContextSetWindowHost installs h, keeping events flowing to SetEmit's receiver.
	// ContextSetEmitHost and ContextFree uninstall it.
	ContextSetWindowHost(ctx Context, h WindowHost)
	// ContextFree destroys the context but not its root.
	ContextFree(ctx Context)

	RasterNew(w, h int) Raster
	RasterFree(r Raster)
	// RasterPixels returns a copy of r's pixels as 0x00RRGGBB, row by row. It is a
	// copy because a view into a module's memory goes stale when that memory grows.
	RasterPixels(r Raster) []uint32
	// RasterView returns r's pixels as B, G, R, A bytes, row by row, without copying.
	// It is valid only until the next Kit call, which can move a module's memory.
	RasterView(r Raster) []byte
	// RasterDamage returns the rectangles the last Render or RenderDamage redrew.
	RasterDamage(r Raster) []Rect
	// Render lays out and draws the whole tree into r.
	Render(ctx Context, r Raster, w, h int)
	// RenderDamage draws what changed, or everything if full. It resizes r to w x h
	// and returns how many regions it redrew, 0 if nothing changed.
	RenderDamage(ctx Context, r Raster, w, h int, full bool) int
	// NeedsRender reports whether anything is waiting to be drawn.
	NeedsRender(ctx Context) bool
	// Animating reports whether the host should keep rendering frames.
	Animating(ctx Context) bool
	// SetScale sets the UI scale, where 1 means 20 px widgets. It is process-wide.
	SetScale(scale float32)
	// ImagePoll hands finished image decodes to the cache and returns how many.
	ImagePoll() int

	// Press and Release send left-button input with no other modifiers.
	Press(ctx Context, x, y int)
	Release(ctx Context, x, y int)
	// PressMods and ReleaseMods are Press and Release with explicit Mod* bits. A host
	// passes the state as the kit documents it: ModButton1 on the press, not the release.
	PressMods(ctx Context, x, y, mods int)
	ReleaseMods(ctx Context, x, y, mods int)
	// Motion, Wheel, Scroll, Key, KeyRelease, Button and ContextMenu send input and
	// return whether anything changed. Scroll's dx and dy are wheel notches, with
	// dx > 0 right and dy > 0 up. keysym is an X keysym name, and text the characters
	// the key typed, "" for none.
	Motion(ctx Context, x, y, mods int) bool
	Wheel(ctx Context, x, y, delta, mods int) bool
	Scroll(ctx Context, x, y int, dx, dy float32, mods int) bool
	Key(ctx Context, text, keysym string, mods int) bool
	KeyRelease(ctx Context, keysym string, mods int) bool
	Button(ctx Context, x, y, button int, release bool, mods int) bool
	ContextMenu(ctx Context, x, y, mods int) bool
	// Timer fires a timer asked for through WindowHost.Schedule.
	Timer(ctx Context, timer int)

	// EvalNumber evaluates a number expression. It returns false if invalid.
	EvalNumber(expr string) (float64, bool)
}

// Host is the window system: windows, the message loop, and waking the loop.
type Host interface {
	// Supported reports whether windows can be opened. It opens the display.
	Supported() bool

	// WindowNew creates a hidden window on ctx. It returns 0 on failure.
	WindowNew(ctx Context, title string, w, h int) Window
	WindowShow(win Window)
	WindowFree(win Window)

	// WakeInit makes the loop call f after a Wake, outside any kit call. It
	// loses wakes sent before it.
	WakeInit(f func())
	// Wake asks a running loop to call WakeInit's function. It is safe from any
	// goroutine and never blocks.
	Wake()
	// Run runs the loop until Quit or until every window is closed. It returns
	// ErrDisplayLost if the window system goes away, on a host that survives that: under
	// Xlib the process ends there.
	Run() error
	Quit()
}

// ExitIfUnwinding guards Go code called from the kit. A panic or Goexit escaping
// it would abandon the kit's frames below, leaving its state corrupt (input scope
// open, pointer grab held), and a recover further up would let the process carry
// on with that. So it ends the process, whatever callers do. Engines defer it
// where the kit calls into Go, and set returned last. It must be deferred
// directly, as recover only works there.
func ExitIfUnwinding(returned *bool) {
	if *returned {
		return
	}

	fmt.Fprintf(os.Stderr, "bui: panic or Goexit in a callback from the kit: %v\n\n%s", recover(), debug.Stack())
	os.Exit(2)
}
