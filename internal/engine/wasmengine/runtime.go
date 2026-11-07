// SPDX-License-Identifier: GPL-2.0-or-later

// Package wasmengine runs BUI's kit as a WebAssembly module in wazero, with no
// cgo. It has no build constraint, so it can link next to the cgo engine and tests
// can compare the two.
package wasmengine

import (
	"context"
	"crypto/rand"
	_ "embed" // for kitModule
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sync"

	"github.com/CWBudde/bui-go/internal/engine"
	"github.com/tetratelabs/wazero"
	"github.com/tetratelabs/wazero/api"
	"github.com/tetratelabs/wazero/experimental"
	"github.com/tetratelabs/wazero/imports/wasi_snapshot_preview1"
)

// kitModule is built by scripts/build-wasm.sh from the BUI commit in BUI_COMMIT.
//
//go:embed bui_kit.wasm
var kitModule []byte

const (
	// memoryLimitPages caps the module's linear memory at 1 GiB, a quarter of what
	// wasm can address. Linear memory never shrinks, so this bounds a runaway UI
	// rather than a normal one: a full-HD raster takes 8 MiB.
	memoryLimitPages = 16384

	// maxCompileWorkers keeps a cold start from swamping the machine, for instance
	// when many test binaries start at once. Four workers already give most of the
	// gain (PLAN.md, Phase 0 results).
	maxCompileWorkers = 4
)

// options are what tests vary. Production uses defaultOptions.
type options struct {
	// interpreter forces wazero's interpreter. Otherwise wazero picks the compiler
	// where it is supported and the interpreter elsewhere.
	interpreter bool
	// cacheDir holds compiled code, if not empty.
	cacheDir         string
	memoryLimitPages uint32
	// fs is what to mount. Production mounts nothing: see moduleConfig.
	fs wazero.FSConfig
}

func defaultOptions() options {
	o := options{memoryLimitPages: memoryLimitPages}

	if dir, err := os.UserCacheDir(); err == nil {
		o.cacheDir = filepath.Join(dir, "bui-go", "wazero")
	}

	return o
}

// module is one instantiated kit. A wazero module is not goroutine-safe, so like
// the kit itself it is UI-thread only.
type module struct {
	ctx     context.Context //nolint:containedctx // one module for the process, never cancelled
	runtime wazero.Runtime
	cache   wazero.CompilationCache
	mod     api.Module
	emit    engine.EmitFunc
	// windowHosts holds each context's window host.
	windowHosts map[engine.Context]engine.WindowHost

	// levels and depth are the export calls in progress: see level.
	levels []*level
	depth  int
}

var (
	loadOnce sync.Once
	loaded   *module
	// loadedEmit is what SetEmit got for the process's module, which it must not
	// load: package bui calls it at init.
	loadedEmit engine.EmitFunc
)

// get returns the process's module, loading it on first use, so that importing
// this package costs nothing until then.
func get() *module {
	loadOnce.Do(func() {
		loaded = mustLoad(kitModule, defaultOptions())
		loaded.emit = loadedEmit
	})

	return loaded
}

// fatalf ends the process. A kit call has no way to report a failure, and after a
// trap the C state is unknown. It is os.Exit and not a panic so that no recover,
// neither a caller's nor wazero's around host functions, can carry on.
func fatalf(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "bui: "+format+"\n", args...)
	os.Exit(2)
}

// mustLoad ends the process on failure: the embedded module loading is not
// something a caller can work around.
func mustLoad(bin []byte, o options) *module {
	m, err := load(bin, o)
	if err != nil {
		fatalf("cannot load the wasm kit: %v", err)
	}

	return m
}

// load compiles and instantiates bin. The cache is only an optimization, and
// wazero fails the compile if the cache cannot be read or written, not just when
// it cannot be opened. So any failure with a cache is retried without one.
func load(bin []byte, o options) (*module, error) {
	ctx := experimental.WithCompilationWorkers(context.Background(), compileWorkers())

	if o.cacheDir != "" {
		cache, err := wazero.NewCompilationCacheWithDir(o.cacheDir)
		if err == nil {
			m, err := instantiate(ctx, bin, o, cache)
			if err == nil {
				return m, nil
			}

			_ = cache.Close(ctx)
		}
	}

	return instantiate(ctx, bin, o, nil)
}

// compileWorkers follows GOMAXPROCS, not the CPU count: more workers than that add
// no parallelism, only the memory each one compiles with.
func compileWorkers() int {
	return min(maxCompileWorkers, runtime.GOMAXPROCS(0))
}

func instantiate(ctx context.Context, bin []byte, o options, cache wazero.CompilationCache) (*module, error) {
	m := &module{
		ctx:     ctx,
		runtime: wazero.NewRuntimeWithConfig(ctx, runtimeConfig(o, cache)),
		cache:   cache,

		windowHosts: map[engine.Context]engine.WindowHost{},
	}

	if _, err := wasi_snapshot_preview1.Instantiate(ctx, m.runtime); err != nil {
		_ = m.runtime.Close(ctx)

		return nil, fmt.Errorf("wasi: %w", err)
	}

	_, err := m.hostModule().Instantiate(ctx)
	if err != nil {
		_ = m.runtime.Close(ctx)

		return nil, fmt.Errorf("host module: %w", err)
	}

	cfg := moduleConfig()
	if o.fs != nil {
		cfg = cfg.WithFSConfig(o.fs)
	}

	m.mod, err = m.runtime.InstantiateWithConfig(ctx, bin, cfg)
	if err != nil {
		_ = m.runtime.Close(ctx)

		return nil, fmt.Errorf("instantiate: %w", err)
	}

	return m, nil
}

// runtimeConfig enables what bui_kit.wasm needs: V2 for SIMD, and exception
// handling for its exnref setjmp and C++ exceptions. cache may be nil.
func runtimeConfig(o options, cache wazero.CompilationCache) wazero.RuntimeConfig {
	cfg := wazero.NewRuntimeConfig()
	if o.interpreter {
		cfg = wazero.NewRuntimeConfigInterpreter()
	}

	cfg = cfg.
		WithCoreFeatures(api.CoreFeaturesV2 | experimental.CoreFeaturesExceptionHandling).
		WithMemoryLimitPages(o.memoryLimitPages)
	if cache != nil {
		cfg = cfg.WithCompilationCache(cache)
	}

	return cfg
}

// moduleConfig replaces wazero's fake clock, sleep and random source with real
// ones and wires stderr through. It mounts no file system, as the default font is
// embedded in the module. _initialize is the reactor's start function.
func moduleConfig() wazero.ModuleConfig {
	return wazero.NewModuleConfig().
		WithName("").
		WithStartFunctions("_initialize").
		WithStderr(os.Stderr).
		WithSysNanotime().
		WithSysWalltime().
		WithSysNanosleep().
		WithRandSource(rand.Reader)
}

// hostModule is the shim's imports. wazero recovers a panic in a host function and
// returns it from the export call, so each has the guard: see guarded.
func (m *module) hostModule() wazero.HostModuleBuilder {
	i32s := func(n int) []api.ValueType {
		types := make([]api.ValueType, n)
		for i := range types {
			types[i] = api.ValueTypeI32
		}

		return types
	}

	b := m.runtime.NewHostModuleBuilder("bui")
	add := func(name string, f api.GoModuleFunc, params, results []api.ValueType) {
		b.NewFunctionBuilder().WithGoModuleFunction(guarded(f), params, results).Export(name)
	}

	add("emit", m.hostEmit, []api.ValueType{api.ValueTypeI32, api.ValueTypeI32, api.ValueTypeF64}, nil)
	add("host_request", m.hostRequest, i32s(1), nil)
	add("host_set_cursor", m.hostSetCursor, i32s(2), nil)
	add("host_warp", m.hostWarp, i32s(3), nil)
	add("host_clipboard_get", m.hostClipboardGet, i32s(1), i32s(1))
	add("host_clipboard_set", m.hostClipboardSet, i32s(2), nil)
	add("host_schedule", m.hostSchedule, i32s(3), nil)

	return b
}

// guarded ends the process on a panic or Goexit in f: see engine.ExitIfUnwinding.
func guarded(f api.GoModuleFunc) api.GoModuleFunc {
	return func(ctx context.Context, mod api.Module, stack []uint64) {
		returned := false

		defer engine.ExitIfUnwinding(&returned)

		f(ctx, mod, stack)

		returned = true
	}
}

// hostEmit is the shim's bui.emit import.
func (m *module) hostEmit(_ context.Context, _ api.Module, stack []uint64) {
	if m.emit != nil {
		m.emit(api.DecodeU32(stack[0]), int(api.DecodeI32(stack[1])), api.DecodeF64(stack[2]))
	}
}

// windowHost finds the host of the context in stack[0]. The shim installs the
// trampolines only together with an entry, so a missing one is a bug in this package.
func (m *module) windowHost(stack []uint64) engine.WindowHost {
	ctx := engine.Context(api.DecodeU32(stack[0]))

	h, ok := m.windowHosts[ctx]
	if !ok {
		fatalf("wasm kit called the window host of context %#x, which has none", ctx)
	}

	return h
}

func (m *module) hostRequest(_ context.Context, _ api.Module, stack []uint64) {
	m.windowHost(stack).Request()
}

func (m *module) hostSetCursor(_ context.Context, _ api.Module, stack []uint64) {
	m.windowHost(stack).SetCursor(int(api.DecodeI32(stack[1])))
}

func (m *module) hostWarp(_ context.Context, _ api.Module, stack []uint64) {
	m.windowHost(stack).Warp(int(api.DecodeI32(stack[1])), int(api.DecodeI32(stack[2])))
}

// hostClipboardGet returns a string from the module's malloc, which the kit frees,
// or NULL. The export calls nest like any other inside a call in progress.
func (m *module) hostClipboardGet(_ context.Context, _ api.Module, stack []uint64) {
	text, ok := m.windowHost(stack).ClipboardGet()
	stack[0] = api.EncodeU32(0)

	if !ok {
		return
	}

	p := api.DecodeU32(m.call("malloc", strSize(text)))
	if p == 0 {
		return
	}

	m.putStr(p, text)
	stack[0] = api.EncodeU32(p)
}

func (m *module) hostClipboardSet(_ context.Context, _ api.Module, stack []uint64) {
	m.windowHost(stack).ClipboardSet(m.goStr(api.DecodeU32(stack[1])))
}

func (m *module) hostSchedule(_ context.Context, _ api.Module, stack []uint64) {
	m.windowHost(stack).Schedule(int(api.DecodeI32(stack[1])), int(api.DecodeI32(stack[2])))
}

// close releases the module. Only tests load more than one.
func (m *module) close() {
	_ = m.runtime.Close(m.ctx)

	if m.cache != nil {
		_ = m.cache.Close(m.ctx)
	}
}
