// SPDX-License-Identifier: GPL-2.0-or-later

package wasmengine

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/tetratelabs/wazero"
	"github.com/tetratelabs/wazero/api"
	"github.com/tetratelabs/wazero/imports/wasi_snapshot_preview1"
)

// The expected hashes come from a native build of wasm/sjlj_probe.c against the
// same FreeType sources (BUI e3aba90, clang -O2, glibc longjmp). The smooth
// rasterizer is integer-only, so the bitmaps must match bit for bit. Natively each
// of the large glyphs overflows the cell pool more than ten times.
var sjljCases = []struct {
	char rune
	size uint32
	hash uint32
}{
	{'@', 2000, 0x91b90bf9},
	{'&', 2000, 0x7c330dc6},
	{'%', 1000, 0x030d311a},
	{'Ж', 2000, 0xd476ec29},
	{'@', 16, 0x374d6ad0}, // small: no overflow, the plain path
}

// TestSjljFreeTypeOverflow forces FreeType's longjmp out of the rasterizer, on the
// configuration the kit runs with, and checks that it lands: no error, the jump
// counted, and the native bitmap. The module is read at run time so that it stays
// out of production binaries.
func TestSjljFreeTypeOverflow(t *testing.T) {
	bin, err := os.ReadFile(filepath.Join("testdata", "sjlj_probe.wasm"))
	if err != nil {
		t.Fatal(err)
	}

	for _, tc := range []struct {
		name        string
		interpreter bool
	}{{"compiler", false}, {"interpreter", true}} {
		t.Run(tc.name, func(t *testing.T) {
			call := loadSjljProbe(t, bin, tc.interpreter)

			for _, c := range sjljCases {
				before := call("probe_jumps")
				ftErr := call("probe_render", uint64(c.char), uint64(c.size))
				jumps := call("probe_jumps") - before
				hash := call("probe_hash")

				t.Logf("U+%04X at %4d px: FT error %d, %2d longjmps, hash %08x", c.char, c.size, int32(ftErr), jumps, hash)

				if ftErr != 0 {
					t.Errorf("U+%04X at %d px: FreeType error %d", c.char, c.size, int32(ftErr))
				}

				if overflows := c.size >= 1000; overflows != (jumps != 0) {
					t.Errorf("U+%04X at %d px: %d longjmps, so the wrong path was exercised", c.char, c.size, jumps)
				}

				if hash != c.hash {
					t.Errorf("U+%04X at %d px: bitmap hash %08x, native %08x", c.char, c.size, hash, c.hash)
				}
			}
		})
	}
}

// loadSjljProbe instantiates the probe with the kit's configuration and returns a
// function that calls its exports.
func loadSjljProbe(t *testing.T, bin []byte, interpreter bool) func(name string, params ...uint64) uint32 {
	t.Helper()

	ctx := context.Background()

	r := wazero.NewRuntimeWithConfig(ctx, runtimeConfig(options{
		interpreter:      interpreter,
		memoryLimitPages: memoryLimitPages,
	}, nil))

	t.Cleanup(func() { _ = r.Close(ctx) })

	if _, err := wasi_snapshot_preview1.Instantiate(ctx, r); err != nil {
		t.Fatal(err)
	}

	mod, err := r.InstantiateWithConfig(ctx, bin, moduleConfig())
	if err != nil {
		t.Fatal(err)
	}

	return func(name string, params ...uint64) uint32 {
		t.Helper()

		f := mod.ExportedFunction(name)
		if f == nil {
			t.Fatalf("no export %q", name)
		}

		res, err := f.Call(ctx, params...)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}

		return api.DecodeU32(res[0])
	}
}
