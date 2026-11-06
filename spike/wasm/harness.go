// SPDX-License-Identifier: GPL-2.0-or-later

// Package wasmspike is the throwaway Phase 0 harness that runs BUI's kit as
// WebAssembly in wazero and compares it with the cgo build. See PLAN.md.
package wasmspike

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"

	"github.com/tetratelabs/wazero"
	"github.com/tetratelabs/wazero/api"
	"github.com/tetratelabs/wazero/experimental"
	"github.com/tetratelabs/wazero/imports/wasi_snapshot_preview1"
)

// Engine selects wazero's compiler or its interpreter.
type Engine string

// The two wazero engines.
const (
	Compiler    Engine = "compiler"
	Interpreter Engine = "interpreter"
)

// Engines lists the engines every behaviour test runs on.
var Engines = []Engine{Compiler, Interpreter}

// ErrNoExport reports a missing export.
var ErrNoExport = errors.New("export missing")

// ErrNotBuilt reports that scripts/build-wasm.sh has not produced a module.
var ErrNotBuilt = errors.New("module not built; run scripts/build-wasm.sh")

// OutDir is where scripts/build-wasm.sh puts its modules.
func OutDir() string {
	_, file, _, _ := runtime.Caller(0) //nolint:dogsled // only the file is needed

	return filepath.Join(filepath.Dir(file), "..", "..", "build", "wasm", "out")
}

// ReadModule returns the bytes of a module built by scripts/build-wasm.sh.
func ReadModule(name string) ([]byte, error) {
	b, err := os.ReadFile(filepath.Join(OutDir(), name))
	if errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("%s: %w", name, ErrNotBuilt)
	}

	if err != nil {
		return nil, fmt.Errorf("read module: %w", err)
	}

	return b, nil
}

// RuntimeConfig returns the configuration Phase 2 would use: core features v2
// plus the experimental exception handling that exnref setjmp needs. cache may
// be nil.
func RuntimeConfig(engine Engine, cache wazero.CompilationCache) wazero.RuntimeConfig {
	var cfg wazero.RuntimeConfig
	if engine == Interpreter {
		cfg = wazero.NewRuntimeConfigInterpreter()
	} else {
		cfg = wazero.NewRuntimeConfigCompiler()
	}

	cfg = cfg.WithCoreFeatures(api.CoreFeaturesV2 | experimental.CoreFeaturesExceptionHandling)
	if cache != nil {
		cfg = cfg.WithCompilationCache(cache)
	}

	return cfg
}

// ModuleConfig wires real clocks, sleep and randomness, stderr, and no file
// system. _initialize is run as the reactor's start function.
func ModuleConfig() wazero.ModuleConfig {
	return wazero.NewModuleConfig().
		WithName("").
		WithStartFunctions("_initialize").
		WithStderr(os.Stderr).
		WithSysNanotime().
		WithSysWalltime().
		WithSysNanosleep().
		WithRandSource(rand.Reader)
}

// Instance is one instantiated module with its runtime.
type Instance struct {
	Runtime wazero.Runtime
	Module  api.Module
}

// Close closes the runtime and with it the module.
func (in *Instance) Close(ctx context.Context) error {
	if err := in.Runtime.Close(ctx); err != nil {
		return fmt.Errorf("close runtime: %w", err)
	}

	return nil
}

// Instantiate compiles and instantiates bin with WASI and the host modules
// that hostSetup adds (may be nil). modCfg nil means ModuleConfig().
func Instantiate(ctx context.Context, engine Engine, cache wazero.CompilationCache, bin []byte,
	hostSetup func(wazero.Runtime) error, modCfg wazero.ModuleConfig,
) (*Instance, error) {
	if modCfg == nil {
		modCfg = ModuleConfig()
	}

	r := wazero.NewRuntimeWithConfig(ctx, RuntimeConfig(engine, cache))

	if _, err := wasi_snapshot_preview1.Instantiate(ctx, r); err != nil {
		_ = r.Close(ctx)

		return nil, fmt.Errorf("wasi: %w", err)
	}

	if hostSetup != nil {
		if err := hostSetup(r); err != nil {
			_ = r.Close(ctx)

			return nil, err
		}
	}

	compiled, err := r.CompileModule(ctx, bin)
	if err != nil {
		_ = r.Close(ctx)

		return nil, fmt.Errorf("compile: %w", err)
	}

	mod, err := r.InstantiateModule(ctx, compiled, modCfg)
	if err != nil {
		_ = r.Close(ctx)

		return nil, fmt.Errorf("instantiate: %w", err)
	}

	return &Instance{Runtime: r, Module: mod}, nil
}

// Call calls an export and returns its first result. Any error is returned as
// is: in Phase 2 it would be fatal.
func (in *Instance) Call(ctx context.Context, name string, params ...uint64) (uint64, error) {
	f := in.Module.ExportedFunction(name)
	if f == nil {
		return 0, fmt.Errorf("%w: %q", ErrNoExport, name)
	}

	res, err := f.Call(ctx, params...)
	if err != nil {
		return 0, fmt.Errorf("%s: %w", name, err)
	}

	if len(res) == 0 {
		return 0, nil
	}

	return res[0], nil
}
