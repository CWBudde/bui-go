// SPDX-License-Identifier: GPL-2.0-or-later

//go:build cgo && linux && amd64

// Package cgoengine runs BUI's kit from the committed static libraries in lib/.
package cgoengine

/*
#cgo CFLAGS: -I${SRCDIR}/../../../include
#cgo LDFLAGS: -L${SRCDIR}/../../../lib/linux_amd64 -lbui_kit -lbui_gpu -lbui_core -lstdc++ -ldl -lm -lpthread
#include <stdlib.h>
#include "bridge.h"
*/
import "C"

import (
	"unsafe"

	"github.com/CWBudde/bui-go/internal/engine"
)

// Kit and Host implement the engine interfaces. The C kit keeps global state, so
// they hold none of their own.
type (
	Kit  struct{}
	Host struct{}
)

var (
	_ engine.Kit  = Kit{}
	_ engine.Host = Host{}
)

// The event kinds and modifiers must equal the kit's: either difference overflows uint at
// compile time if they do not.
const (
	_ = uint(C.BK_EV_CHANGE-engine.EvChange) + uint(engine.EvChange-C.BK_EV_CHANGE)
	_ = uint(C.BK_EV_CLICK-engine.EvClick) + uint(engine.EvClick-C.BK_EV_CLICK)
	_ = uint(C.BK_SHIFT-engine.ModShift) + uint(engine.ModShift-C.BK_SHIFT)
	_ = uint(C.BK_CTRL-engine.ModCtrl) + uint(engine.ModCtrl-C.BK_CTRL)
	_ = uint(C.BK_ALT-engine.ModAlt) + uint(engine.ModAlt-C.BK_ALT)
	_ = uint(C.BK_BUTTON1-engine.ModButton1) + uint(engine.ModButton1-C.BK_BUTTON1)
)

// The kit's callbacks, set once from the UI thread and read on it.
var (
	emitFunc engine.EmitFunc
	wakeFunc func()
	// windowHosts holds each context's window host.
	windowHosts = map[engine.Context]engine.WindowHost{}
)

// ptr turns an opaque reference back into its C pointer. It points into C memory,
// which the Go runtime neither moves nor frees.
func ptr[R ~uintptr](r R) unsafe.Pointer {
	return *(*unsafe.Pointer)(unsafe.Pointer(&r))
}

func node(n engine.Node) *C.BKNode            { return (*C.BKNode)(ptr(n)) }
func context(ctx engine.Context) *C.BKContext { return (*C.BKContext)(ptr(ctx)) }
func raster(r engine.Raster) *C.BuiRaster     { return (*C.BuiRaster)(ptr(r)) }
func window(win engine.Window) *C.BKWindow    { return (*C.BKWindow)(ptr(win)) }

func nodeRef(c *C.BKNode) engine.Node {
	return engine.Node(unsafe.Pointer(c))
}

func cstr(s string) (*C.char, func()) {
	cs := C.CString(s)
	return cs, func() { C.free(unsafe.Pointer(cs)) }
}

// SetEmit implements engine.Kit.
func (Kit) SetEmit(f engine.EmitFunc) { emitFunc = f }

// NodeNew implements engine.Kit.
func (Kit) NodeNew(typ string) engine.Node {
	cs, free := cstr(typ)
	defer free()
	return nodeRef(C.BK_node_new(cs))
}

// NodeFree implements engine.Kit.
func (Kit) NodeFree(n engine.Node) { C.BK_node_free(node(n)) }

// NodeSetID implements engine.Kit.
func (Kit) NodeSetID(n engine.Node, id uint32) { C.bui_node_set_id(node(n), C.uint32_t(id)) }

// NodeID implements engine.Kit.
func (Kit) NodeID(n engine.Node) uint32 { return uint32(C.bui_node_id(node(n))) }

// NodeParent implements engine.Kit.
func (Kit) NodeParent(n engine.Node) engine.Node { return nodeRef(C.BK_node_parent(node(n))) }

// NodeAdd implements engine.Kit.
func (Kit) NodeAdd(parent, child engine.Node) bool {
	return C.BK_node_add(node(parent), node(child), -1) != 0
}

// NodeChildCount implements engine.Kit.
func (Kit) NodeChildCount(n engine.Node) int { return int(C.BK_node_child_count(node(n))) }

// NodeChild implements engine.Kit.
func (Kit) NodeChild(n engine.Node, i int) engine.Node {
	return nodeRef(C.BK_node_child(node(n), C.int(i)))
}

// NodeRect implements engine.Kit.
func (Kit) NodeRect(n engine.Node) (x, y, w, h int) {
	var r [4]C.int
	C.BK_node_rect(node(n), &r[0])
	return int(r[0]), int(r[1]), int(r[2]), int(r[3])
}

// NodeRelayout implements engine.Kit.
func (Kit) NodeRelayout(n engine.Node) { C.BK_node_relayout(node(n)) }

// SetNum implements engine.Kit.
func (Kit) SetNum(n engine.Node, key string, v float64) bool {
	ck, free := cstr(key)
	defer free()
	return C.BK_set_num(node(n), ck, C.double(v)) != 0
}

// SetStr implements engine.Kit.
func (Kit) SetStr(n engine.Node, key, v string) bool {
	ck, freeKey := cstr(key)
	defer freeKey()
	cv, freeValue := cstr(v)
	defer freeValue()
	return C.BK_set_str(node(n), ck, cv) != 0
}

// SetStrs implements engine.Kit. The slice is Go memory holding only C pointers,
// which cgo allows.
func (Kit) SetStrs(n engine.Node, key string, vs []string) bool {
	ck, free := cstr(key)
	defer free()
	cvs := make([]*C.char, len(vs))
	for i, v := range vs {
		cv, freeValue := cstr(v)
		defer freeValue()
		cvs[i] = cv
	}
	return C.BK_set_strs(node(n), ck, unsafe.SliceData(cvs), C.int(len(cvs))) != 0
}

// GetNum implements engine.Kit.
func (Kit) GetNum(n engine.Node, key string) float64 {
	ck, free := cstr(key)
	defer free()
	return float64(C.BK_get_num(node(n), ck))
}

// GetStr implements engine.Kit.
func (Kit) GetStr(n engine.Node, key string) string {
	ck, free := cstr(key)
	defer free()
	return C.GoString(C.BK_get_str(node(n), ck))
}

// GeomSetPack implements engine.Kit.
func (Kit) GeomSetPack(n engine.Node) { C.BK_geom_set_manager(node(n), C.BK_GEOM_PACK) }

// GeomSet implements engine.Kit.
func (Kit) GeomSet(n engine.Node, key string, v float64) {
	ck, free := cstr(key)
	defer free()
	C.BK_geom_set(node(n), ck, C.double(v))
}

// ContextNew implements engine.Kit.
func (Kit) ContextNew(root engine.Node) engine.Context {
	return engine.Context(unsafe.Pointer(C.BK_context_new(node(root))))
}

// ContextRoot implements engine.Kit.
func (Kit) ContextRoot(ctx engine.Context) engine.Node {
	return nodeRef(C.BK_context_root(context(ctx)))
}

// ContextSetEmitHost implements engine.Kit.
func (Kit) ContextSetEmitHost(ctx engine.Context) {
	C.bui_context_set_emit_host(context(ctx))
	delete(windowHosts, ctx)
}

// ContextSetWindowHost implements engine.Kit.
func (Kit) ContextSetWindowHost(ctx engine.Context, h engine.WindowHost) {
	windowHosts[ctx] = h
	C.bui_context_set_window_host(context(ctx))
}

// ContextFree implements engine.Kit.
func (Kit) ContextFree(ctx engine.Context) {
	C.BK_context_free(context(ctx))
	delete(windowHosts, ctx)
}

// RasterNew implements engine.Kit.
func (Kit) RasterNew(w, h int) engine.Raster {
	return engine.Raster(unsafe.Pointer(C.BUI_raster_create(C.int(w), C.int(h))))
}

// RasterFree implements engine.Kit.
func (Kit) RasterFree(r engine.Raster) { C.BUI_raster_free(raster(r)) }

// RasterPixels implements engine.Kit.
func (Kit) RasterPixels(r engine.Raster) []uint32 {
	c := raster(r)
	n := int(C.BUI_raster_width(c)) * int(C.BUI_raster_height(c))
	pixels := C.BUI_raster_pixels(c)
	if pixels == nil || n == 0 {
		return nil
	}
	return append([]uint32(nil), unsafe.Slice((*uint32)(unsafe.Pointer(pixels)), n)...)
}

// Render implements engine.Kit.
func (Kit) Render(ctx engine.Context, r engine.Raster, w, h int) {
	C.BK_render(context(ctx), raster(r), C.int(w), C.int(h), 1)
}

// Press implements engine.Kit.
func (Kit) Press(ctx engine.Context, x, y int) {
	C.BK_press(context(ctx), C.int(x), C.int(y), C.BK_BUTTON1)
}

// Release implements engine.Kit.
func (Kit) Release(ctx engine.Context, x, y int) {
	C.BK_release(context(ctx), C.int(x), C.int(y), 0)
}

// PressMods implements engine.Kit.
func (Kit) PressMods(ctx engine.Context, x, y, mods int) {
	C.BK_press(context(ctx), C.int(x), C.int(y), C.int(mods))
}

// ReleaseMods implements engine.Kit.
func (Kit) ReleaseMods(ctx engine.Context, x, y, mods int) {
	C.BK_release(context(ctx), C.int(x), C.int(y), C.int(mods))
}

// Motion implements engine.Kit.
func (Kit) Motion(ctx engine.Context, x, y, mods int) bool {
	return C.BK_motion(context(ctx), C.int(x), C.int(y), C.int(mods)) != 0
}

// Wheel implements engine.Kit.
func (Kit) Wheel(ctx engine.Context, x, y, delta, mods int) bool {
	return C.BK_wheel(context(ctx), C.int(x), C.int(y), C.int(delta), C.int(mods)) != 0
}

// Scroll implements engine.Kit.
func (Kit) Scroll(ctx engine.Context, x, y int, dx, dy float32, mods int) bool {
	return C.BK_scroll(context(ctx), C.int(x), C.int(y), C.float(dx), C.float(dy), C.int(mods)) != 0
}

// Key implements engine.Kit.
func (Kit) Key(ctx engine.Context, text, keysym string, mods int) bool {
	ct, freeText := cstr(text)
	defer freeText()
	cs, freeSym := cstr(keysym)
	defer freeSym()
	return C.BK_key(context(ctx), ct, cs, C.int(mods)) != 0
}

// KeyRelease implements engine.Kit.
func (Kit) KeyRelease(ctx engine.Context, keysym string, mods int) bool {
	cs, free := cstr(keysym)
	defer free()
	return C.BK_key_release(context(ctx), cs, C.int(mods)) != 0
}

// Button implements engine.Kit.
func (Kit) Button(ctx engine.Context, x, y, button int, release bool, mods int) bool {
	r := C.int(0)
	if release {
		r = 1
	}
	return C.BK_button(context(ctx), C.int(x), C.int(y), C.int(button), r, C.int(mods)) != 0
}

// ContextMenu implements engine.Kit.
func (Kit) ContextMenu(ctx engine.Context, x, y, mods int) bool {
	return C.BK_context_menu(context(ctx), C.int(x), C.int(y), C.int(mods)) != 0
}

// Timer implements engine.Kit.
func (Kit) Timer(ctx engine.Context, timer int) { C.BK_timer(context(ctx), C.int(timer)) }

// RenderDamage implements engine.Kit.
func (Kit) RenderDamage(ctx engine.Context, r engine.Raster, w, h int, full bool) int {
	f := C.int(0)
	if full {
		f = 1
	}
	return int(C.BK_render(context(ctx), raster(r), C.int(w), C.int(h), f))
}

// NeedsRender implements engine.Kit.
func (Kit) NeedsRender(ctx engine.Context) bool { return C.BK_needs_render(context(ctx)) != 0 }

// Animating implements engine.Kit.
func (Kit) Animating(ctx engine.Context) bool { return C.BK_animating(context(ctx)) != 0 }

// SetScale implements engine.Kit.
func (Kit) SetScale(scale float32) { C.BK_set_scale(C.float(scale)) }

// ImagePoll implements engine.Kit.
func (Kit) ImagePoll() int { return int(C.BK_image_poll()) }

// RasterView implements engine.Kit.
func (Kit) RasterView(r engine.Raster) []byte {
	c := raster(r)
	n := 4 * int(C.BUI_raster_width(c)) * int(C.BUI_raster_height(c))
	pixels := C.BUI_raster_pixels(c)
	if pixels == nil || n == 0 {
		return nil
	}
	return unsafe.Slice((*byte)(unsafe.Pointer(pixels)), n)
}

// RasterDamage implements engine.Kit.
func (Kit) RasterDamage(r engine.Raster) []engine.Rect {
	var rects *[4]C.int
	n := int(C.BUI_raster_damage(raster(r), &rects)) //nolint:gocritic // cgo expands &rects
	if n <= 0 || rects == nil {
		return nil
	}
	damage := make([]engine.Rect, n)
	for i, v := range unsafe.Slice(rects, n) {
		damage[i] = engine.Rect{X: int(v[0]), Y: int(v[1]), W: int(v[2]), H: int(v[3])}
	}
	return damage
}

// EvalNumber implements engine.Kit.
func (Kit) EvalNumber(expr string) (float64, bool) {
	cs, free := cstr(expr)
	defer free()
	var v C.double
	if C.BK_eval_number(cs, &v) == 0 {
		return 0, false
	}
	return float64(v), true
}

// Supported implements engine.Host.
func (Host) Supported() bool { return C.BK_window_supported() != 0 }

// WindowNew implements engine.Host.
func (Host) WindowNew(ctx engine.Context, title string, w, h int) engine.Window {
	ct, free := cstr(title)
	defer free()
	return engine.Window(unsafe.Pointer(C.BK_window_new(context(ctx), ct, C.int(w), C.int(h))))
}

// WindowShow implements engine.Host.
func (Host) WindowShow(win engine.Window) { C.BK_window_show(window(win)) }

// WindowFree implements engine.Host.
func (Host) WindowFree(win engine.Window) { C.BK_window_free(window(win)) }

// WakeInit implements engine.Host.
func (Host) WakeInit(f func()) {
	wakeFunc = f
	C.bui_wake_init()
}

// Wake implements engine.Host.
func (Host) Wake() { C.BK_window_wake() }

// Run implements engine.Host.
func (Host) Run() error {
	C.BK_window_run()

	return nil
}

// Quit implements engine.Host.
func (Host) Quit() { C.BK_window_quit() }

// buiGoEmit receives every kit event for a node with an id.
//
//export buiGoEmit
func buiGoEmit(id C.uint32_t, kind C.int, a C.double) {
	returned := false
	defer engine.ExitIfUnwinding(&returned)
	emitFunc(uint32(id), int(kind), float64(a))
	returned = true
}

// windowHostOf finds the window host of the context in a trampoline's data. The bridge
// installs the trampolines only together with an entry.
func windowHostOf(data unsafe.Pointer) engine.WindowHost {
	return windowHosts[engine.Context(data)]
}

//export buiGoHostRequest
func buiGoHostRequest(data unsafe.Pointer) {
	returned := false
	defer engine.ExitIfUnwinding(&returned)
	windowHostOf(data).Request()
	returned = true
}

//export buiGoHostSetCursor
func buiGoHostSetCursor(data unsafe.Pointer, cursor C.int) {
	returned := false
	defer engine.ExitIfUnwinding(&returned)
	windowHostOf(data).SetCursor(int(cursor))
	returned = true
}

//export buiGoHostWarp
func buiGoHostWarp(data unsafe.Pointer, x, y C.int) {
	returned := false
	defer engine.ExitIfUnwinding(&returned)
	windowHostOf(data).Warp(int(x), int(y))
	returned = true
}

// buiGoHostClipboardGet returns malloc'd memory, which the kit frees, or NULL.
//
//export buiGoHostClipboardGet
func buiGoHostClipboardGet(data unsafe.Pointer) *C.char {
	returned := false
	defer engine.ExitIfUnwinding(&returned)
	var out *C.char
	if text, ok := windowHostOf(data).ClipboardGet(); ok {
		out = C.CString(text)
	}
	returned = true
	return out
}

//export buiGoHostClipboardSet
func buiGoHostClipboardSet(data unsafe.Pointer, text *C.char) {
	returned := false
	defer engine.ExitIfUnwinding(&returned)
	windowHostOf(data).ClipboardSet(C.GoString(text))
	returned = true
}

//export buiGoHostSchedule
func buiGoHostSchedule(data unsafe.Pointer, ms, timer C.int) {
	returned := false
	defer engine.ExitIfUnwinding(&returned)
	windowHostOf(data).Schedule(int(ms), int(timer))
	returned = true
}

// buiGoWake is the loop's wake callback. It runs inside BK_window_run but outside
// any kit call.
//
//export buiGoWake
func buiGoWake() {
	returned := false
	defer engine.ExitIfUnwinding(&returned)
	wakeFunc()
	returned = true
}
