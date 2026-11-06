// SPDX-License-Identifier: GPL-2.0-or-later

//go:build cgo && linux && amd64

package wasmspike

/*
#cgo CFLAGS: -I${SRCDIR}/../../include
#cgo LDFLAGS: -L${SRCDIR}/../../lib/linux_amd64 -lbui_kit -lbui_gpu -lbui_core -lstdc++ -ldl -lm -lpthread
#include <stdlib.h>
#include "nativekit.h"
*/
import "C"

import (
	"unsafe"
)

// NativeAvailable reports whether this build has the cgo kit.
const NativeAvailable = true

// NativeKit drives the committed static libraries in lib/linux_amd64, the
// same ones package bui links, and reads raster pixels that bui's public API
// does not expose. Like the kit itself it is single-threaded.
type NativeKit struct {
	nodes []*C.BKNode
	emit  func(n Node, kind int, a float64)
}

// nativeCurrent receives the native kit's events. One NativeKit at a time.
var nativeCurrent *NativeKit

// NewNativeKit returns the cgo kit.
func NewNativeKit() *NativeKit {
	k := &NativeKit{}
	nativeCurrent = k

	return k
}

//export spikeNativeEmit
func spikeNativeEmit(id C.uint32_t, kind C.int, a C.double) {
	k := nativeCurrent
	if k != nil && k.emit != nil && id != 0 {
		k.emit(Node(id-1), int(kind), float64(a))
	}
}

// Name implements Kit.
func (k *NativeKit) Name() string { return "native" }

func (k *NativeKit) node(n Node) *C.BKNode {
	c := k.nodes[n]
	if c == nil {
		panic("use of a freed node")
	}

	return c
}

func setRef(c *C.BKNode, id uint32) { C.spike_native_set_id(c, C.uint32_t(id)) }

// NewNode implements Kit.
func (k *NativeKit) NewNode(typ string) Node {
	cs := C.CString(typ)
	defer C.free(unsafe.Pointer(cs))

	c := C.BK_node_new(cs)
	if c == nil {
		panic("unknown node type " + typ)
	}

	n := Node(len(k.nodes))
	k.nodes = append(k.nodes, c)
	setRef(c, uint32(n+1))

	return n
}

// Add implements Kit.
func (k *NativeKit) Add(parent, child Node) {
	if C.BK_node_add(k.node(parent), k.node(child), -1) == 0 {
		panic("BK_node_add refused the node")
	}
}

// SetNum implements Kit.
func (k *NativeKit) SetNum(n Node, key string, v float64) bool {
	ck := C.CString(key)
	defer C.free(unsafe.Pointer(ck))

	return C.BK_set_num(k.node(n), ck, C.double(v)) != 0
}

// SetStr implements Kit.
func (k *NativeKit) SetStr(n Node, key, v string) bool {
	ck, cv := C.CString(key), C.CString(v)
	defer C.free(unsafe.Pointer(ck))
	defer C.free(unsafe.Pointer(cv))

	return C.BK_set_str(k.node(n), ck, cv) != 0
}

// Num implements Kit.
func (k *NativeKit) Num(n Node, key string) float64 {
	ck := C.CString(key)
	defer C.free(unsafe.Pointer(ck))

	return float64(C.BK_get_num(k.node(n), ck))
}

// Str implements Kit.
func (k *NativeKit) Str(n Node, key string) string {
	ck := C.CString(key)
	defer C.free(unsafe.Pointer(ck))

	return C.GoString(C.BK_get_str(k.node(n), ck))
}

// Pack implements Kit.
func (k *NativeKit) Pack(n Node, o PackOptions) {
	c := k.node(n)
	C.BK_geom_set_manager(c, geomPack)

	for _, kv := range packPairs(o) {
		ck := C.CString(kv.key)
		C.BK_geom_set(c, ck, C.double(kv.v))
		C.free(unsafe.Pointer(ck))
	}
}

// Rect implements Kit.
func (k *NativeKit) Rect(n Node) [4]int {
	var r [4]C.int

	C.BK_node_rect(k.node(n), &r[0])

	return [4]int{int(r[0]), int(r[1]), int(r[2]), int(r[3])}
}

// SetImageSource implements Kit.
func (k *NativeKit) SetImageSource(n Node, src string) {
	cs := C.CString(src)
	defer C.free(unsafe.Pointer(cs))

	C.BK_node_set_image_source(k.node(n), cs, fitFill)
}

// FreeTree implements Kit.
func (k *NativeKit) FreeTree(root Node) {
	c := k.node(root)
	k.freeChildren(c)
	setRef(c, 0)
	C.BK_node_free(c)
	k.nodes[root] = nil
}

func (k *NativeKit) freeChildren(c *C.BKNode) {
	for i := int(C.BK_node_child_count(c)) - 1; i >= 0; i-- {
		child := C.BK_node_child(c, C.int(i))
		if id := uint32(C.spike_native_id(child)); id != 0 {
			k.FreeTree(Node(id - 1))
		} else {
			k.freeChildren(child)
		}
	}
}

// OnEmit implements Kit.
func (k *NativeKit) OnEmit(f func(n Node, kind int, a float64)) { k.emit = f }

// EvalNumber implements Kit.
func (k *NativeKit) EvalNumber(expr string) (float64, bool) {
	cs := C.CString(expr)
	defer C.free(unsafe.Pointer(cs))

	var v C.double
	if C.BK_eval_number(cs, &v) == 0 {
		return 0, false
	}

	return float64(v), true
}

// ImagePending implements Kit.
func (k *NativeKit) ImagePending() int { return int(C.spike_native_image_pending()) }

// ImagePoll implements Kit.
func (k *NativeKit) ImagePoll() int { return int(C.BK_image_poll()) }

// Close implements Kit.
func (k *NativeKit) Close() {
	if nativeCurrent == k {
		nativeCurrent = nil
	}
}

type nativeHeadless struct {
	ctx  *C.BKContext
	r    *C.BuiRaster
	w, h int
}

// NewHeadless implements Kit.
func (k *NativeKit) NewHeadless(root Node, w, h int) Headless {
	ctx := C.BK_context_new(k.node(root))
	C.spike_native_set_emit_host(ctx)

	return &nativeHeadless{ctx: ctx, r: C.BUI_raster_create(C.int(w), C.int(h)), w: w, h: h}
}

func (h *nativeHeadless) Render() { C.BK_render(h.ctx, h.r, C.int(h.w), C.int(h.h), 1) }

func (h *nativeHeadless) RenderDamage() { C.BK_render(h.ctx, h.r, C.int(h.w), C.int(h.h), 0) }

func (h *nativeHeadless) Invalidate() { C.BUI_raster_invalidate(h.r) }

func (h *nativeHeadless) Pixels() []uint32 {
	p := C.BUI_raster_pixels(h.r)

	return append([]uint32(nil), unsafe.Slice((*uint32)(unsafe.Pointer(p)), h.w*h.h)...)
}

func (h *nativeHeadless) Press(x, y int) { C.BK_press(h.ctx, C.int(x), C.int(y), C.BK_BUTTON1) }

func (h *nativeHeadless) Release(x, y int) { C.BK_release(h.ctx, C.int(x), C.int(y), C.BK_BUTTON1) }

func (h *nativeHeadless) Motion(x, y int) { C.BK_motion(h.ctx, C.int(x), C.int(y), 0) }

func (h *nativeHeadless) Close() {
	C.BK_context_free(h.ctx)
	C.BUI_raster_free(h.r)
}
