// SPDX-License-Identifier: GPL-2.0-or-later

package wasmengine

import (
	"bytes"
	"encoding/binary"
	"math"

	"github.com/CWBudde/bui-go/internal/engine"
	"github.com/tetratelabs/wazero/api"
)

// The kit's constants the calls need, as in BUI_kit.h. A test checks them there.
const (
	bkGeomPack = 1
	bkButton1  = 8
	// renderFull is BK_render's last argument: redraw every pixel.
	renderFull = 1
)

const (
	// callSlots is more than any export's parameters and results.
	callSlots = 8
	// minScratch is the least a level's scratch buffer holds, enough for most calls
	// never to grow it.
	minScratch = 1024
)

// level is what one nesting level of export calls owns. A handler runs inside the
// export call that emitted, and re-enters the module from there, so nothing an
// export call is still using may be shared with the calls of its handlers:
//
//   - funcs: a wazero api.Function runs on its own native stack, from the top of it,
//     so re-entering the same one would overwrite the frames of the call in progress.
//   - stack: CallWithStack's parameters and results.
//   - scratch: the call's strings and out-parameters, which C can read at any time
//     until the call returns.
//
// Levels are reused, and they stay as many as handlers ever nested.
type level struct {
	funcs       map[string]api.Function
	stack       [callSlots]uint64
	scratch     uint32
	scratchSize uint32
}

// level returns the level of the next export call. Levels are pointers so that
// adding one moves nothing a call in progress holds.
func (m *module) level() *level {
	for m.depth >= len(m.levels) {
		m.levels = append(m.levels, &level{funcs: map[string]api.Function{}})
	}

	return m.levels[m.depth]
}

// call calls an export and returns its result, or garbage if it has none. Any error
// is fatal, as the C state is unknown after a trap.
func (m *module) call(name string, params ...uint64) uint64 {
	l := m.level()

	f, ok := l.funcs[name]
	if !ok {
		f = m.mod.ExportedFunction(name)
		if f == nil {
			fatalf("the wasm kit has no export %q", name)
		}

		l.funcs[name] = f
	}

	stack := l.stack[:max(len(params), 1)]
	copy(stack, params)

	m.depth++
	err := f.CallWithStack(m.ctx, stack)
	m.depth--

	if err != nil {
		fatalf("wasm kit call %s: %v", name, err)
	}

	return stack[0]
}

// scratch returns this level's buffer, holding at least n bytes. It is valid until
// the next scratch call at this level, and what it held before does not survive
// growing. malloc returning 0 is fatal for now: PLAN.md, Phase 4, decides what to do
// past the memory limit.
func (m *module) scratch(n uint64) uint32 {
	l := m.level()
	if n <= uint64(l.scratchSize) {
		return l.scratch
	}

	if n > math.MaxUint32 {
		fatalf("wasm kit call needs %d bytes of arguments, more than module memory can hold", n)
	}

	// Freed first, so that malloc can reuse the room.
	if l.scratch != 0 {
		m.call("free", api.EncodeU32(l.scratch))
	}

	size := min(max(n, 2*uint64(l.scratchSize), minScratch), math.MaxUint32)

	p := api.DecodeU32(m.call("malloc", size))
	if p == 0 && size > n {
		size = n
		p = api.DecodeU32(m.call("malloc", size))
	}

	if p == 0 {
		fatalf("wasm kit out of memory: malloc(%d) failed", size)
	}

	l.scratch, l.scratchSize = p, uint32(size)

	return p
}

// putStr writes s as a C string at p and returns the address after it. A NUL in s
// ends the string for C, as it does with C.CString.
func (m *module) putStr(p uint32, s string) uint32 {
	end := p + uint32(len(s)) //nolint:gosec // it fit in the buffer scratch returned

	mem := m.mod.Memory()
	if !mem.WriteString(p, s) || !mem.WriteByte(end, 0) {
		fatalf("wasm kit string at %#x out of range", p)
	}

	return end + 1
}

// cstr puts s into this level's scratch buffer.
func (m *module) cstr(s string) uint32 {
	p := m.scratch(strSize(s))
	m.putStr(p, s)

	return p
}

// strSize is what s takes as a C string.
func strSize(s string) uint64 { return uint64(len(s)) + 1 }

// goStr copies the C string at p, where 0 is "" as with C.GoString. It holds a view
// of the memory only until the copy, as the next module call can grow it.
func (m *module) goStr(p uint32) string {
	if p == 0 {
		return ""
	}

	mem := m.mod.Memory()

	b, ok := mem.Read(p, mem.Size()-min(p, mem.Size()))
	if !ok {
		fatalf("wasm kit string at %#x out of range", p)
	}

	end := bytes.IndexByte(b, 0)
	if end < 0 {
		fatalf("wasm kit string at %#x is not terminated", p)
	}

	return string(b[:end])
}

// readI32 reads a C int.
func (m *module) readI32(p uint32) int {
	v, ok := m.mod.Memory().ReadUint32Le(p)
	if !ok {
		fatalf("wasm kit result at %#x out of range", p)
	}

	return int(api.DecodeI32(uint64(v)))
}

// i32 encodes a C int. A plain uint64(v) would sign-extend a negative v past the 32
// bits of the parameter. It truncates like C.int.
func i32(v int) uint64 {
	return api.EncodeI32(int32(v)) //nolint:gosec // as C.int(v)
}

// ptr encodes an opaque reference. Each one came from the module, so it fits.
func ptr[R ~uintptr](r R) uint64 {
	return api.EncodeU32(uint32(r))
}

// truth decodes a C int used as a boolean.
func truth(res uint64) bool { return api.DecodeI32(res) != 0 }

// Kit implements engine.Kit on a module. The zero value uses the process's module,
// loading it at the first call that needs it.
type Kit struct {
	m *module
}

var _ engine.Kit = Kit{}

// NewInterpreterKit returns a Kit on its own module on wazero's interpreter, for
// tests that run the engines side by side.
func NewInterpreterKit() Kit {
	return Kit{m: mustLoad(kitModule, options{interpreter: true, memoryLimitPages: memoryLimitPages})}
}

func (k Kit) get() *module {
	if k.m != nil {
		return k.m
	}

	return get()
}

// SetEmit implements engine.Kit. On the zero Kit it does not load the module, as
// package bui calls it at init and importing must cost nothing.
func (k Kit) SetEmit(f engine.EmitFunc) {
	if k.m != nil {
		k.m.emit = f
		return
	}

	loadedEmit = f

	if loaded != nil {
		loaded.emit = f
	}
}

// NodeNew implements engine.Kit.
func (k Kit) NodeNew(typ string) engine.Node {
	m := k.get()

	return engine.Node(api.DecodeU32(m.call("BK_node_new", api.EncodeU32(m.cstr(typ)))))
}

// NodeFree implements engine.Kit.
func (k Kit) NodeFree(n engine.Node) { k.get().call("BK_node_free", ptr(n)) }

// NodeSetID implements engine.Kit.
func (k Kit) NodeSetID(n engine.Node, id uint32) {
	k.get().call("bui_node_set_id", ptr(n), api.EncodeU32(id))
}

// NodeID implements engine.Kit.
func (k Kit) NodeID(n engine.Node) uint32 {
	return api.DecodeU32(k.get().call("bui_node_id", ptr(n)))
}

// NodeParent implements engine.Kit.
func (k Kit) NodeParent(n engine.Node) engine.Node {
	return engine.Node(api.DecodeU32(k.get().call("BK_node_parent", ptr(n))))
}

// NodeAdd implements engine.Kit.
func (k Kit) NodeAdd(parent, child engine.Node) bool {
	return truth(k.get().call("BK_node_add", ptr(parent), ptr(child), i32(-1)))
}

// NodeChildCount implements engine.Kit.
func (k Kit) NodeChildCount(n engine.Node) int {
	return int(api.DecodeI32(k.get().call("BK_node_child_count", ptr(n))))
}

// NodeChild implements engine.Kit.
func (k Kit) NodeChild(n engine.Node, i int) engine.Node {
	return engine.Node(api.DecodeU32(k.get().call("BK_node_child", ptr(n), i32(i))))
}

// NodeRect implements engine.Kit.
func (k Kit) NodeRect(n engine.Node) (x, y, w, h int) {
	m := k.get()
	r := m.scratch(4 * 4)
	m.call("BK_node_rect", ptr(n), api.EncodeU32(r))

	return m.readI32(r), m.readI32(r + 4), m.readI32(r + 8), m.readI32(r + 12)
}

// SetNum implements engine.Kit.
func (k Kit) SetNum(n engine.Node, key string, v float64) bool {
	m := k.get()

	return truth(m.call("BK_set_num", ptr(n), api.EncodeU32(m.cstr(key)), api.EncodeF64(v)))
}

// SetStr implements engine.Kit.
func (k Kit) SetStr(n engine.Node, key, v string) bool {
	m := k.get()
	ck := m.scratch(strSize(key) + strSize(v))
	cv := m.putStr(ck, key)
	m.putStr(cv, v)

	return truth(m.call("BK_set_str", ptr(n), api.EncodeU32(ck), api.EncodeU32(cv)))
}

// SetStrs implements engine.Kit. The buffer holds the array of pointers first, which
// keeps it aligned, then the key and the values.
func (k Kit) SetStrs(n engine.Node, key string, vs []string) bool {
	const pointerSize = 4

	m := k.get()

	size := strSize(key) + pointerSize*uint64(len(vs))
	for _, v := range vs {
		size += strSize(v)
	}

	array := m.scratch(size)
	ck := array + pointerSize*uint32(len(vs)) //nolint:gosec // scratch checked the size
	next := m.putStr(ck, key)

	mem := m.mod.Memory()
	for i, v := range vs {
		if !mem.WriteUint32Le(array+pointerSize*uint32(i), next) {
			fatalf("wasm kit string list at %#x out of range", array)
		}

		next = m.putStr(next, v)
	}

	return truth(m.call("BK_set_strs", ptr(n), api.EncodeU32(ck), api.EncodeU32(array), i32(len(vs))))
}

// GetNum implements engine.Kit.
func (k Kit) GetNum(n engine.Node, key string) float64 {
	m := k.get()

	return api.DecodeF64(m.call("BK_get_num", ptr(n), api.EncodeU32(m.cstr(key))))
}

// GetStr implements engine.Kit.
func (k Kit) GetStr(n engine.Node, key string) string {
	m := k.get()

	return m.goStr(api.DecodeU32(m.call("BK_get_str", ptr(n), api.EncodeU32(m.cstr(key)))))
}

// GeomSetPack implements engine.Kit.
func (k Kit) GeomSetPack(n engine.Node) {
	k.get().call("BK_geom_set_manager", ptr(n), i32(bkGeomPack))
}

// GeomSet implements engine.Kit.
func (k Kit) GeomSet(n engine.Node, key string, v float64) {
	m := k.get()
	m.call("BK_geom_set", ptr(n), api.EncodeU32(m.cstr(key)), api.EncodeF64(v))
}

// ContextNew implements engine.Kit.
func (k Kit) ContextNew(root engine.Node) engine.Context {
	return engine.Context(api.DecodeU32(k.get().call("BK_context_new", ptr(root))))
}

// ContextSetEmitHost implements engine.Kit.
func (k Kit) ContextSetEmitHost(ctx engine.Context) {
	k.get().call("bui_context_set_emit_host", ptr(ctx))
}

// ContextFree implements engine.Kit.
func (k Kit) ContextFree(ctx engine.Context) { k.get().call("BK_context_free", ptr(ctx)) }

// RasterNew implements engine.Kit.
func (k Kit) RasterNew(w, h int) engine.Raster {
	return engine.Raster(api.DecodeU32(k.get().call("BUI_raster_create", i32(w), i32(h))))
}

// RasterFree implements engine.Kit.
func (k Kit) RasterFree(r engine.Raster) { k.get().call("BUI_raster_free", ptr(r)) }

// RasterPixels implements engine.Kit. The pixels are read after the last module
// call, so no view of the memory outlives a possible growth.
func (k Kit) RasterPixels(r engine.Raster) []uint32 {
	const pixelSize = 4

	m := k.get()
	w := int(api.DecodeI32(m.call("BUI_raster_width", ptr(r))))
	h := int(api.DecodeI32(m.call("BUI_raster_height", ptr(r))))
	pixels := api.DecodeU32(m.call("BUI_raster_pixels", ptr(r)))

	if pixels == 0 || w <= 0 || h <= 0 {
		return nil
	}

	size := pixelSize * uint64(w) * uint64(h)
	if size > math.MaxUint32 {
		fatalf("wasm kit raster of %d x %d is too large", w, h)
	}

	b, ok := m.mod.Memory().Read(pixels, uint32(size)) //nolint:gosec // checked above
	if !ok {
		fatalf("wasm kit raster at %#x out of range", pixels)
	}

	out := make([]uint32, size/pixelSize)
	for i := range out {
		out[i] = binary.LittleEndian.Uint32(b[pixelSize*i:])
	}

	return out
}

// Render implements engine.Kit.
func (k Kit) Render(ctx engine.Context, r engine.Raster, w, h int) {
	k.get().call("BK_render", ptr(ctx), ptr(r), i32(w), i32(h), i32(renderFull))
}

// Press implements engine.Kit.
func (k Kit) Press(ctx engine.Context, x, y int) {
	k.get().call("BK_press", ptr(ctx), i32(x), i32(y), i32(bkButton1))
}

// Release implements engine.Kit.
func (k Kit) Release(ctx engine.Context, x, y int) {
	k.get().call("BK_release", ptr(ctx), i32(x), i32(y), i32(bkButton1))
}

// EvalNumber implements engine.Kit. The result goes first in the buffer, which keeps
// it aligned.
func (k Kit) EvalNumber(expr string) (float64, bool) {
	const doubleSize = 8

	m := k.get()
	out := m.scratch(doubleSize + strSize(expr))
	cs := out + doubleSize
	m.putStr(cs, expr)

	if !truth(m.call("BK_eval_number", api.EncodeU32(cs), api.EncodeU32(out))) {
		return 0, false
	}

	v, ok := m.mod.Memory().ReadFloat64Le(out)
	if !ok {
		fatalf("wasm kit result at %#x out of range", out)
	}

	return v, true
}
