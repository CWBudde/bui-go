// SPDX-License-Identifier: GPL-2.0-or-later

package wasmengine

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/tetratelabs/wazero"
	"github.com/tetratelabs/wazero/api"
	"github.com/tetratelabs/wazero/imports/wasi_snapshot_preview1"
)

const pageSize = 65536

// sharedCache spares the tests that are not about caching a compile each.
var sharedCache string

func TestMain(m *testing.M) {
	// Nothing has asked for the kit yet, so importing the package must not have
	// loaded it. Checked here, as a test could run after another one has.
	if loaded != nil {
		fmt.Fprintln(os.Stderr, "the module was loaded before its first use")
		os.Exit(1)
	}

	dir, err := os.MkdirTemp("", "bui-wasmengine-test-")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}

	sharedCache = dir
	code := m.Run()
	_ = os.RemoveAll(dir)

	os.Exit(code)
}

func loadForTest(t *testing.T, o options) *module {
	t.Helper()

	m, err := load(kitModule, o)
	if err != nil {
		t.Fatalf("load: %v", err)
	}

	t.Cleanup(m.close)

	return m
}

// callExport calls an export the way the Kit calls of a later phase will.
func callExport(t *testing.T, m *module, name string, params ...uint64) uint64 {
	t.Helper()

	f := m.mod.ExportedFunction(name)
	if f == nil {
		t.Fatalf("no export %q", name)
	}

	res, err := f.Call(m.ctx, params...)
	if err != nil {
		t.Fatalf("%s: %v", name, err)
	}

	if len(res) == 0 {
		return 0
	}

	return res[0]
}

func evalNumber(t *testing.T, m *module, expr string) (float64, bool) {
	t.Helper()

	s := uint32(callExport(t, m, "malloc", uint64(len(expr)+1)))
	out := uint32(callExport(t, m, "malloc", 8))

	if s == 0 || out == 0 {
		t.Fatal("malloc failed")
	}

	defer callExport(t, m, "free", uint64(s))
	defer callExport(t, m, "free", uint64(out))

	if !m.mod.Memory().WriteString(s, expr+"\x00") {
		t.Fatal("string out of range")
	}

	if callExport(t, m, "BK_eval_number", uint64(s), uint64(out)) == 0 {
		return 0, false
	}

	bits, ok := m.mod.Memory().ReadUint64Le(out)
	if !ok {
		t.Fatal("result out of range")
	}

	return math.Float64frombits(bits), true
}

func TestModuleRunsOnBothEngines(t *testing.T) {
	for _, tc := range []struct {
		name        string
		interpreter bool
	}{{"compiler", false}, {"interpreter", true}} {
		t.Run(tc.name, func(t *testing.T) {
			m := loadForTest(t, options{
				interpreter:      tc.interpreter,
				cacheDir:         sharedCache,
				memoryLimitPages: memoryLimitPages,
			})

			got, ok := evalNumber(t, m, "2*pi")
			if !ok || math.Abs(got-2*math.Pi) > 1e-9 {
				t.Fatalf("2*pi = %v, %v", got, ok)
			}

			if _, ok := evalNumber(t, m, "2*"); ok {
				t.Fatal("an invalid expression evaluated")
			}
		})
	}
}

func TestGetLoadsOnceInTheUserCacheDir(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CACHE_HOME", filepath.Join(home, "cache"))
	t.Setenv("LocalAppData", filepath.Join(home, "cache"))

	loadOnce = sync.Once{}
	loaded = nil

	t.Cleanup(func() {
		loaded.close()

		loadOnce = sync.Once{}
		loaded = nil
	})

	first := get()
	if first == nil || get() != first {
		t.Fatal("get did not return one module")
	}

	userCache, err := os.UserCacheDir()
	if err != nil {
		t.Fatal(err)
	}

	dir := filepath.Join(userCache, "bui-go", "wazero")

	if !supportsCompiler() {
		return
	}

	// Only the compiler writes the cache, so this also shows that it was picked.
	if n := countFiles(t, dir); n == 0 {
		t.Fatalf("no compiled module in %s", dir)
	}
}

// supportsCompiler is the part of wazero's own check that matters on the machines
// that run this test.
func supportsCompiler() bool {
	return runtime.GOARCH == "amd64" || runtime.GOARCH == "arm64"
}

func countFiles(t *testing.T, dir string) int {
	t.Helper()

	n := 0

	err := filepath.WalkDir(dir, func(_ string, d os.DirEntry, err error) error {
		if err == nil && !d.IsDir() {
			n++
		}

		return err
	})
	if err != nil {
		t.Fatalf("walk %s: %v", dir, err)
	}

	return n
}

// walkCache visits what a load left in dir, deepest first, so that f can take away
// the permissions of a directory after handling its contents.
func walkCache(t *testing.T, dir string, f func(path string, isDir bool) error) {
	t.Helper()

	type entry struct {
		path  string
		isDir bool
	}

	var entries []entry

	err := filepath.WalkDir(dir, func(path string, d os.DirEntry, err error) error {
		if err == nil {
			entries = append(entries, entry{path, d.IsDir()})
		}

		return err
	})
	if err != nil {
		t.Fatalf("walk %s: %v", dir, err)
	}

	for _, e := range slices.Backward(entries) {
		if err := f(e.path, e.isDir); err != nil {
			t.Fatal(err)
		}
	}
}

func TestCacheIsReusedAndFailuresFallBack(t *testing.T) {
	t.Run("reused", func(t *testing.T) {
		dir := t.TempDir()
		o := options{cacheDir: dir, memoryLimitPages: memoryLimitPages}

		for range 2 {
			m := loadForTest(t, o)
			if got, ok := evalNumber(t, m, "1+1"); !ok || got != 2 {
				t.Fatalf("1+1 = %v, %v", got, ok)
			}
		}
	})

	t.Run("path is a file", func(t *testing.T) {
		file := filepath.Join(t.TempDir(), "file")
		if err := os.WriteFile(file, nil, 0o600); err != nil {
			t.Fatal(err)
		}

		loadForTest(t, options{cacheDir: file, memoryLimitPages: memoryLimitPages})
	})

	// wazero creates its directory when the cache opens and writes to it only after
	// compiling, so a directory it can open but not write fails the compile itself.
	t.Run("read-only", func(t *testing.T) {
		if os.Getuid() == 0 {
			t.Skip("root ignores directory permissions")
		}

		dir := t.TempDir()
		o := options{cacheDir: dir, memoryLimitPages: memoryLimitPages}

		m, err := load(kitModule, o)
		if err != nil {
			t.Fatal(err)
		}

		m.close()

		walkCache(t, dir, func(path string, isDir bool) error {
			if isDir {
				return os.Chmod(path, 0o500)
			}

			return os.Remove(path)
		})
		t.Cleanup(func() {
			walkCache(t, dir, func(path string, isDir bool) error {
				if isDir {
					return os.Chmod(path, 0o700)
				}

				return nil
			})
		})

		loadForTest(t, o)
	})

	t.Run("corrupt", func(t *testing.T) {
		dir := t.TempDir()
		o := options{cacheDir: dir, memoryLimitPages: memoryLimitPages}

		m, err := load(kitModule, o)
		if err != nil {
			t.Fatal(err)
		}

		m.close()

		walkCache(t, dir, func(path string, isDir bool) error {
			if isDir {
				return nil
			}

			return os.WriteFile(path, []byte("garbage"), 0o600)
		})

		loadForTest(t, o)
	})

	t.Run("none", func(t *testing.T) {
		loadForTest(t, options{memoryLimitPages: memoryLimitPages})
	})
}

func TestCompileWorkersFollowGOMAXPROCS(t *testing.T) {
	t.Cleanup(func(saved int) func() {
		return func() { runtime.GOMAXPROCS(saved) }
	}(runtime.GOMAXPROCS(0)))

	for _, tc := range []struct{ procs, want int }{{1, 1}, {2, 2}, {maxCompileWorkers, maxCompileWorkers}, {64, maxCompileWorkers}} {
		runtime.GOMAXPROCS(tc.procs)

		if got := compileWorkers(); got != tc.want {
			t.Errorf("GOMAXPROCS %d: %d workers, want %d", tc.procs, got, tc.want)
		}
	}
}

func TestMemoryLimit(t *testing.T) {
	if got := defaultOptions().memoryLimitPages; got != memoryLimitPages {
		t.Fatalf("default limit = %d pages, want %d", got, memoryLimitPages)
	}

	probe := loadForTest(t, options{cacheDir: sharedCache, memoryLimitPages: memoryLimitPages})
	limit := probe.mod.Memory().Size()/pageSize + 32

	m := loadForTest(t, options{cacheDir: sharedCache, memoryLimitPages: limit})

	if p := callExport(t, m, "malloc", 16*pageSize); p == 0 {
		t.Fatal("malloc failed below the limit")
	}

	// Past the limit malloc sees memory.grow fail: no trap, just a null pointer.
	if p := callExport(t, m, "malloc", 64*pageSize); p != 0 {
		t.Fatalf("malloc past the limit = %d, want 0", p)
	}
}

// wasiProbe builds what the kit never exports: direct use of the WASI imports. It
// is a module with one page of exported memory and functions that forward their
// parameters:
//
//	now(ptr)                   clock_time_get(realtime, 0, ptr)
//	rand(ptr, n)               random_get(ptr, n)
//	poll(in, out, n, nevents)  poll_oneoff(in, out, n, nevents)
//	write(fd, iovs, n, nw)     fd_write(fd, iovs, n, nw)
//	prestat(fd, ptr)           fd_prestat_get(fd, ptr)
func wasiProbe() []byte {
	const (
		i32 = 0x7f
		i64 = 0x7e
	)

	str := func(s string) []byte { return append([]byte{byte(len(s))}, s...) }
	section := func(id byte, body ...[]byte) []byte {
		var b []byte
		for _, x := range body {
			b = append(b, x...)
		}

		// A two-byte LEB128 length is enough for this module.
		return append([]byte{id, byte(len(b)) | 0x80, byte(len(b) >> 7)}, b...)
	}
	funcType := func(params ...byte) []byte {
		return append(append([]byte{0x60, byte(len(params))}, params...), 1, i32)
	}
	imp := func(name string, typ byte) []byte {
		return append(append(str("wasi_snapshot_preview1"), str(name)...), 0, typ)
	}
	export := func(name string, kind, idx byte) []byte { return append(str(name), kind, idx) }
	code := func(instrs ...byte) []byte {
		body := append(append([]byte{0}, instrs...), 0x0b)

		return append([]byte{byte(len(body))}, body...)
	}

	module := []byte{0, 'a', 's', 'm', 1, 0, 0, 0}
	for _, s := range [][]byte{
		section(1, []byte{4}, funcType(i32, i64, i32), funcType(i32, i32), funcType(i32, i32, i32, i32), funcType(i32)),
		section(2, []byte{5}, imp("clock_time_get", 0), imp("random_get", 1), imp("poll_oneoff", 2),
			imp("fd_write", 2), imp("fd_prestat_get", 1)),
		section(3, []byte{5, 3, 1, 2, 2, 1}),
		section(5, []byte{1, 0, 1}),
		section(7, []byte{6}, export("memory", 2, 0), export("now", 0, 5), export("rand", 0, 6), export("poll", 0, 7),
			export("write", 0, 8), export("prestat", 0, 9)),
		section(10, []byte{5},
			code(0x41, 0, 0x42, 0, 0x20, 0, 0x10, 0),
			code(0x20, 0, 0x20, 1, 0x10, 1),
			code(0x20, 0, 0x20, 1, 0x20, 2, 0x20, 3, 0x10, 2),
			code(0x20, 0, 0x20, 1, 0x20, 2, 0x20, 3, 0x10, 3),
			code(0x20, 0, 0x20, 1, 0x10, 4)),
	} {
		module = append(module, s...)
	}

	return module
}

// loadProbe instantiates wasiProbe with the kit's configuration.
func loadProbe(ctx context.Context, t *testing.T, interpreter bool) api.Module {
	t.Helper()

	r := wazero.NewRuntimeWithConfig(ctx, runtimeConfig(options{
		interpreter:      interpreter,
		memoryLimitPages: memoryLimitPages,
	}, nil))

	t.Cleanup(func() { _ = r.Close(ctx) })

	if _, err := wasi_snapshot_preview1.Instantiate(ctx, r); err != nil {
		t.Fatal(err)
	}

	mod, err := r.InstantiateWithConfig(ctx, wasiProbe(), moduleConfig())
	if err != nil {
		t.Fatal(err)
	}

	return mod
}

func callProbe(ctx context.Context, t *testing.T, mod api.Module, name string, params ...uint64) {
	t.Helper()

	res, err := mod.ExportedFunction(name).Call(ctx, params...)
	if err != nil {
		t.Fatalf("%s: %v", name, err)
	}

	if res[0] != 0 {
		t.Fatalf("%s: errno %d", name, res[0])
	}
}

// wazero's defaults are a fixed clock, a sleep that returns at once and a seeded
// random source. The module must get the real ones.
func TestSystemSourcesAreReal(t *testing.T) {
	ctx := context.Background()

	for _, tc := range []struct {
		name        string
		interpreter bool
	}{{"compiler", false}, {"interpreter", true}} {
		t.Run(tc.name, func(t *testing.T) {
			mod := loadProbe(ctx, t, tc.interpreter)
			mem := mod.Memory()

			callProbe(ctx, t, mod, "now", 0)

			ns, _ := mem.ReadUint64Le(0)
			if d := time.Since(time.Unix(0, int64(ns))); d.Abs() > time.Minute {
				t.Fatalf("the module's clock is off by %v", d)
			}

			callProbe(ctx, t, mod, "rand", 0, 32)

			first, _ := mem.Read(0, 32)
			firstCopy := string(first)

			again := loadProbe(ctx, t, tc.interpreter)
			callProbe(ctx, t, again, "rand", 0, 32)

			second, _ := again.Memory().Read(0, 32)
			if firstCopy == string(second) {
				t.Fatal("two modules drew the same random bytes")
			}

			const sleep = 30 * time.Millisecond

			// One relative clock subscription: tag 0 at 8, realtime clock at 16,
			// timeout at 24.
			sub := make([]byte, 48)
			binary.LittleEndian.PutUint64(sub[24:], uint64(sleep))
			mem.Write(0, sub)

			start := time.Now()

			callProbe(ctx, t, mod, "poll", 0, 64, 1, 128)

			if got := time.Since(start); got < sleep {
				t.Fatalf("a %v sleep took %v", sleep, got)
			}
		})
	}
}

func TestModuleStderrReachesProcessStderr(t *testing.T) {
	ctx := context.Background()

	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}

	saved := os.Stderr
	os.Stderr = w
	mod := loadProbe(ctx, t, false)
	os.Stderr = saved

	const msg = "from the module"

	// One iovec at 0, the text at 64, the count written at 32. Descriptor 2 is stderr.
	mem := mod.Memory()
	mem.WriteUint32Le(0, 64)
	mem.WriteUint32Le(4, uint32(len(msg)))
	mem.WriteString(64, msg)
	callProbe(ctx, t, mod, "write", 2, 0, 1, 32)

	if err := w.Close(); err != nil {
		t.Fatal(err)
	}

	got, err := io.ReadAll(r)
	if err != nil {
		t.Fatal(err)
	}

	if string(got) != msg {
		t.Fatalf("stderr got %q, want %q", got, msg)
	}
}

// A mount would be the first preopen, on descriptor 3.
func TestNoFileSystemIsMounted(t *testing.T) {
	const errnoBadf = 8

	ctx := context.Background()
	mod := loadProbe(ctx, t, false)

	res, err := mod.ExportedFunction("prestat").Call(ctx, 3, 0)
	if err != nil {
		t.Fatal(err)
	}

	if res[0] != errnoBadf {
		t.Fatalf("fd_prestat_get(3) = errno %d, want EBADF (%d)", res[0], errnoBadf)
	}
}

func TestEmitReachesTheReceiver(t *testing.T) {
	var got struct {
		id   uint32
		kind int
		a    float64
	}

	m := &module{emit: func(id uint32, kind int, a float64) { got.id, got.kind, got.a = id, kind, a }}
	m.hostEmit(context.Background(), nil, []uint64{7, api.EncodeI32(2), api.EncodeF64(1.5)})

	if got.id != 7 || got.kind != 2 || got.a != 1.5 {
		t.Fatalf("emit received %+v", got)
	}
}

const childEnv = "BUI_WASMENGINE_TEST_CHILD"

// TestChildProcess does what ends the process when another test asks for it.
func TestChildProcess(t *testing.T) {
	switch os.Getenv(childEnv) {
	case "":
		t.Skip("only runs as a subprocess")
	case "emit-panic":
		// wazero recovers panics around host functions, and an unrecovered panic
		// exits with status 2 by itself, so recover as wazero does: only the guard
		// can then end the process.
		func() {
			defer func() { _ = recover() }()

			m := &module{emit: func(uint32, int, float64) { panic("boom") }}
			m.hostEmit(context.Background(), nil, []uint64{1, 0, 0})
		}()
	case "bad-module":
		_ = mustLoad([]byte("not wasm"), options{memoryLimitPages: memoryLimitPages})
	}

	t.Fatal("the process survived")
}

// Both end the process: a recover further up, or wazero's own recover around host
// functions, must not let it carry on.
func TestFatalPathsExit(t *testing.T) {
	for _, child := range []string{"emit-panic", "bad-module"} {
		t.Run(child, func(t *testing.T) {
			cmd := exec.Command(os.Args[0], "-test.run=^TestChildProcess$", "-test.count=1")

			cmd.Env = append(os.Environ(), childEnv+"="+child)

			out, err := cmd.CombinedOutput()

			var exit *exec.ExitError
			if !errors.As(err, &exit) || exit.ExitCode() != 2 {
				t.Fatalf("child: %v, want exit status 2\n%s", err, out)
			}
		})
	}
}

func TestGarbageModuleIsAnError(t *testing.T) {
	m, err := load([]byte("not wasm"), options{cacheDir: t.TempDir(), memoryLimitPages: memoryLimitPages})
	if err == nil {
		m.close()
		t.Fatal("a garbage module loaded")
	}
}
