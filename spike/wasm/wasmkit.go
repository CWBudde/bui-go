// SPDX-License-Identifier: GPL-2.0-or-later

package wasmspike

import (
	"context"
	"encoding/binary"
	"fmt"
	"math"

	"github.com/tetratelabs/wazero"
	"github.com/tetratelabs/wazero/api"
)

// WasmKit runs bui_kit.wasm in wazero. Strings and out-parameters go through
// the module's malloc and free.
type WasmKit struct {
	ctx    context.Context //nolint:containedctx // one module, one context: a spike
	engine Engine
	in     *Instance
	mem    api.Memory
	fn     map[string]api.Function
	nodes  []uint32 // Node -> BKNode*; 0 once freed
	emit   func(n Node, kind int, a float64)
	stacks [][8]uint64 // one per nesting level: handlers re-enter call
	depth  int
}

// NewWasmKit instantiates bin, which must be bui_kit.wasm. cache and modCfg
// may be nil.
func NewWasmKit(ctx context.Context, engine Engine, cache wazero.CompilationCache, bin []byte,
	modCfg wazero.ModuleConfig,
) (*WasmKit, error) {
	k := &WasmKit{ctx: ctx, engine: engine, fn: map[string]api.Function{}}

	in, err := Instantiate(ctx, engine, cache, bin, func(r wazero.Runtime) error {
		_, err := r.NewHostModuleBuilder("bui").
			NewFunctionBuilder().
			WithGoModuleFunction(api.GoModuleFunc(k.hostEmit),
				[]api.ValueType{api.ValueTypeI32, api.ValueTypeI32, api.ValueTypeF64}, nil).
			WithParameterNames("id", "kind", "a").
			Export("emit").
			Instantiate(ctx)
		if err != nil {
			return fmt.Errorf("host module: %w", err)
		}

		return nil
	}, modCfg)
	if err != nil {
		return nil, err
	}

	k.in = in
	k.mem = in.Module.Memory()

	return k, nil
}

// hostEmit is the shim's bui.emit import. Handlers may call back into the
// module from here: wazero allows re-entering the same module.
func (k *WasmKit) hostEmit(_ context.Context, _ api.Module, stack []uint64) {
	id := api.DecodeU32(stack[0])
	kind := int(api.DecodeI32(stack[1]))
	a := api.DecodeF64(stack[2])

	if k.emit != nil && id != 0 {
		k.emit(Node(id-1), kind, a)
	}
}

// Name implements Kit.
func (k *WasmKit) Name() string { return "wasm-" + string(k.engine) }

// Instance returns the runtime and module.
func (k *WasmKit) Instance() *Instance { return k.in }

// call calls an export with up to 8 parameters and returns its first result.
// An error is a kit trap, which Phase 2 treats as fatal; here it panics.
func (k *WasmKit) call(name string, params ...uint64) uint64 {
	f, ok := k.fn[name]
	if !ok {
		f = k.in.Module.ExportedFunction(name)
		if f == nil {
			panic(fmt.Sprintf("bui_kit.wasm has no export %q", name))
		}

		k.fn[name] = f
	}

	// CallWithStack avoids an allocation per call; the stack must hold
	// max(params, results). A handler running inside this call re-enters, so
	// each nesting level has its own.
	if k.depth == len(k.stacks) {
		k.stacks = append(k.stacks, [8]uint64{})
	}

	stack := k.stacks[k.depth][:max(len(params), 1)]
	copy(stack, params)

	k.depth++
	err := f.CallWithStack(k.ctx, stack)
	k.depth--

	if err != nil {
		panic(fmt.Sprintf("%s: %v", name, err))
	}

	return stack[0]
}

// Call exposes call for diagnostics in tests.
func (k *WasmKit) Call(name string, params ...uint64) uint64 { return k.call(name, params...) }

// cstr copies s into module memory, NUL-terminated. Free it with k.free.
func (k *WasmKit) cstr(s string) uint32 {
	p := uint32(k.call("malloc", uint64(len(s)+1)))
	if p == 0 {
		panic("malloc failed")
	}

	buf, ok := k.mem.Read(p, uint32(len(s)+1))
	if !ok {
		panic("malloc returned memory out of range")
	}

	copy(buf, s)
	buf[len(s)] = 0

	return p
}

func (k *WasmKit) free(p uint32) { k.call("free", uint64(p)) }

func (k *WasmKit) gostr(p uint32) string {
	if p == 0 {
		return ""
	}

	var b []byte

	for {
		c, ok := k.mem.ReadByte(p)
		if !ok || c == 0 {
			return string(b)
		}

		b = append(b, c)
		p++
	}
}

func (k *WasmKit) node(n Node) uint64 {
	c := k.nodes[n]
	if c == 0 {
		panic("use of a freed node")
	}

	return uint64(c)
}

// NewNode implements Kit.
func (k *WasmKit) NewNode(typ string) Node {
	s := k.cstr(typ)
	c := uint32(k.call("BK_node_new", uint64(s)))
	k.free(s)

	if c == 0 {
		panic("unknown node type " + typ)
	}

	n := Node(len(k.nodes))
	k.nodes = append(k.nodes, c)
	k.call("bui_node_set_id", uint64(c), uint64(n+1))

	return n
}

// Add implements Kit.
func (k *WasmKit) Add(parent, child Node) {
	if k.call("BK_node_add", k.node(parent), k.node(child), api.EncodeI32(-1)) == 0 {
		panic("BK_node_add refused the node")
	}
}

// SetNum implements Kit.
func (k *WasmKit) SetNum(n Node, key string, v float64) bool {
	s := k.cstr(key)
	defer k.free(s)

	return k.call("BK_set_num", k.node(n), uint64(s), api.EncodeF64(v)) != 0
}

// SetStr implements Kit.
func (k *WasmKit) SetStr(n Node, key, v string) bool {
	s, t := k.cstr(key), k.cstr(v)
	defer k.free(s)
	defer k.free(t)

	return k.call("BK_set_str", k.node(n), uint64(s), uint64(t)) != 0
}

// Num implements Kit.
func (k *WasmKit) Num(n Node, key string) float64 {
	s := k.cstr(key)
	defer k.free(s)

	return api.DecodeF64(k.call("BK_get_num", k.node(n), uint64(s)))
}

// Str implements Kit.
func (k *WasmKit) Str(n Node, key string) string {
	s := k.cstr(key)
	defer k.free(s)

	return k.gostr(uint32(k.call("BK_get_str", k.node(n), uint64(s))))
}

// Pack implements Kit.
func (k *WasmKit) Pack(n Node, o PackOptions) {
	c := k.node(n)
	k.call("BK_geom_set_manager", c, geomPack)

	for _, kv := range packPairs(o) {
		s := k.cstr(kv.key)
		k.call("BK_geom_set", c, uint64(s), api.EncodeF64(float64(kv.v)))
		k.free(s)
	}
}

type packPair struct {
	key string
	v   int
}

func packPairs(o PackOptions) []packPair {
	expand := 0
	if o.Expand {
		expand = 1
	}

	return []packPair{
		{"side", o.Side},
		{"fill", o.Fill},
		{"expand", expand},
		{"padx0", o.PadX0},
		{"padx1", o.PadX1},
		{"pady0", o.PadY0},
		{"pady1", o.PadY1},
	}
}

// Rect implements Kit.
func (k *WasmKit) Rect(n Node) [4]int {
	p := uint32(k.call("malloc", 16))
	defer k.free(p)

	k.call("BK_node_rect", k.node(n), uint64(p))

	b, _ := k.mem.Read(p, 16)

	var r [4]int
	for i := range r {
		r[i] = int(int32(binary.LittleEndian.Uint32(b[4*i:])))
	}

	return r
}

// SetImageSource implements Kit.
func (k *WasmKit) SetImageSource(n Node, src string) {
	s := k.cstr(src)
	defer k.free(s)

	k.call("BK_node_set_image_source", k.node(n), uint64(s), fitFill)
}

// FreeTree implements Kit: children first, as node.go does, since BK_node_free
// only detaches children that carry a ref.
func (k *WasmKit) FreeTree(root Node) {
	k.freeChildren(uint32(k.node(root)))
	c := k.node(root)
	k.call("bui_node_set_id", c, 0)
	k.call("BK_node_free", c)
	k.nodes[root] = 0
}

func (k *WasmKit) freeChildren(c uint32) {
	for i := int(int32(k.call("BK_node_child_count", uint64(c)))) - 1; i >= 0; i-- {
		child := uint32(k.call("BK_node_child", uint64(c), uint64(i)))
		if id := uint32(k.call("bui_node_id", uint64(child))); id != 0 {
			k.FreeTree(Node(id - 1))
		} else {
			k.freeChildren(child)
		}
	}
}

// OnEmit implements Kit.
func (k *WasmKit) OnEmit(f func(n Node, kind int, a float64)) { k.emit = f }

// EvalNumber implements Kit.
func (k *WasmKit) EvalNumber(expr string) (float64, bool) {
	s := k.cstr(expr)
	defer k.free(s)

	out := uint32(k.call("malloc", 8))
	defer k.free(out)

	if k.call("BK_eval_number", uint64(s), uint64(out)) == 0 {
		return 0, false
	}

	v, _ := k.mem.ReadUint64Le(out)

	return math.Float64frombits(v), true
}

// ImagePending implements Kit.
func (k *WasmKit) ImagePending() int { return int(int32(k.call("bui_image_pending"))) }

// ImagePoll implements Kit.
func (k *WasmKit) ImagePoll() int { return int(int32(k.call("BK_image_poll"))) }

// Close implements Kit.
func (k *WasmKit) Close() { _ = k.in.Close(k.ctx) }

// wasmHeadless is a BKContext and a BuiRaster in the module.
type wasmHeadless struct {
	k      *WasmKit
	ctx, r uint64
	w, h   int
}

// NewHeadless implements Kit.
func (k *WasmKit) NewHeadless(root Node, w, h int) Headless {
	ctx := k.call("BK_context_new", k.node(root))
	k.call("bui_context_set_emit_host", ctx)
	r := k.call("BUI_raster_create", uint64(w), uint64(h))

	return &wasmHeadless{k: k, ctx: ctx, r: r, w: w, h: h}
}

func (h *wasmHeadless) Render() {
	h.k.call("BK_render", h.ctx, h.r, uint64(h.w), uint64(h.h), 1)
}

func (h *wasmHeadless) RenderDamage() {
	h.k.call("BK_render", h.ctx, h.r, uint64(h.w), uint64(h.h), 0)
}

func (h *wasmHeadless) Invalidate() { h.k.call("BUI_raster_invalidate", h.r) }

// Pixels copies the raster: a view would go stale at the next memory.grow.
func (h *wasmHeadless) Pixels() []uint32 {
	p := uint32(h.k.call("BUI_raster_pixels", h.r))
	n := h.w * h.h

	b, ok := h.k.mem.Read(p, uint32(4*n))
	if !ok {
		panic("raster out of range")
	}

	px := make([]uint32, n)
	for i := range px {
		px[i] = binary.LittleEndian.Uint32(b[4*i:])
	}

	return px
}

func (h *wasmHeadless) Press(x, y int) {
	h.k.call("BK_press", h.ctx, uint64(x), uint64(y), button1)
}

func (h *wasmHeadless) Release(x, y int) {
	h.k.call("BK_release", h.ctx, uint64(x), uint64(y), button1)
}

func (h *wasmHeadless) Motion(x, y int) {
	h.k.call("BK_motion", h.ctx, uint64(x), uint64(y), 0)
}

func (h *wasmHeadless) Close() {
	h.k.call("BK_context_free", h.ctx)
	h.k.call("BUI_raster_free", h.r)
}
