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

// The event kinds must equal the kit's: either difference overflows uint at
// compile time if they do not.
const (
	_ = uint(C.BK_EV_CHANGE-engine.EvChange) + uint(engine.EvChange-C.BK_EV_CHANGE)
	_ = uint(C.BK_EV_CLICK-engine.EvClick) + uint(engine.EvClick-C.BK_EV_CLICK)
)

// The kit's callbacks, set once from the UI thread and read on it.
var (
	emitFunc engine.EmitFunc
	wakeFunc func()
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

// ContextSetEmitHost implements engine.Kit.
func (Kit) ContextSetEmitHost(ctx engine.Context) { C.bui_context_set_emit_host(context(ctx)) }

// ContextFree implements engine.Kit.
func (Kit) ContextFree(ctx engine.Context) { C.BK_context_free(context(ctx)) }

// RasterNew implements engine.Kit.
func (Kit) RasterNew(w, h int) engine.Raster {
	return engine.Raster(unsafe.Pointer(C.BUI_raster_create(C.int(w), C.int(h))))
}

// RasterFree implements engine.Kit.
func (Kit) RasterFree(r engine.Raster) { C.BUI_raster_free(raster(r)) }

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
	C.BK_release(context(ctx), C.int(x), C.int(y), C.BK_BUTTON1)
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
func (Host) Run() { C.BK_window_run() }

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
