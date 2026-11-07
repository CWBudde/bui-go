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
}

var (
	loadOnce sync.Once
	loaded   *module
)

// get returns the process's module, loading it on first use, so that importing
// this package costs nothing until then.
func get() *module {
	loadOnce.Do(func() { loaded = mustLoad(kitModule, defaultOptions()) })

	return loaded
}

// mustLoad ends the process on failure. A bindings call has no way to report one,
// and the embedded module loading is not something a caller can work around. It
// is os.Exit and not a panic so that no recover can carry on without a kit.
func mustLoad(bin []byte, o options) *module {
	m, err := load(bin, o)
	if err != nil {
		fmt.Fprintf(os.Stderr, "bui: cannot load the wasm kit: %v\n", err)
		os.Exit(2)
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
	}

	if _, err := wasi_snapshot_preview1.Instantiate(ctx, m.runtime); err != nil {
		_ = m.runtime.Close(ctx)

		return nil, fmt.Errorf("wasi: %w", err)
	}

	_, err := m.runtime.NewHostModuleBuilder("bui").
		NewFunctionBuilder().
		WithGoModuleFunction(api.GoModuleFunc(m.hostEmit),
			[]api.ValueType{api.ValueTypeI32, api.ValueTypeI32, api.ValueTypeF64}, nil).
		WithParameterNames("id", "kind", "a").
		Export("emit").
		Instantiate(ctx)
	if err != nil {
		_ = m.runtime.Close(ctx)

		return nil, fmt.Errorf("host module: %w", err)
	}

	m.mod, err = m.runtime.InstantiateWithConfig(ctx, bin, moduleConfig())
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

// hostEmit is the shim's bui.emit import. wazero recovers a panic in a host
// function and returns it from the export call, so the guard has to be here.
func (m *module) hostEmit(_ context.Context, _ api.Module, stack []uint64) {
	returned := false

	defer engine.ExitIfUnwinding(&returned)

	if m.emit != nil {
		m.emit(api.DecodeU32(stack[0]), int(api.DecodeI32(stack[1])), api.DecodeF64(stack[2]))
	}

	returned = true
}

// close releases the module. Only tests load more than one.
func (m *module) close() {
	_ = m.runtime.Close(m.ctx)

	if m.cache != nil {
		_ = m.cache.Close(m.ctx)
	}
}
