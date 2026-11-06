// SPDX-License-Identifier: GPL-2.0-or-later

package wasmspike

import (
	"context"
	"errors"
	"testing"
)

// skipIfNotBuilt turns a missing module into a skip with a clear message.
func skipIfNotBuilt(tb testing.TB, err error) {
	tb.Helper()

	if errors.Is(err, ErrNotBuilt) {
		tb.Skipf("skipping: %v", err)
	}

	if err != nil {
		tb.Fatal(err)
	}
}

// The expected hashes come from a native build of wasm/sjlj_probe.c against
// the same FreeType sources (BUI e3aba90, clang -O2, glibc longjmp). The smooth
// rasterizer is integer-only, so the bitmaps must match bit for bit. Natively
// each of these glyphs overflows the cell pool more than ten times.
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

// TestSjljFreeTypeOverflow forces FreeType's longjmp out of the rasterizer and
// checks that it lands: no error, the jump counted, and the native bitmap.
func TestSjljFreeTypeOverflow(t *testing.T) {
	bin, err := ReadModule("sjlj_probe.wasm")
	skipIfNotBuilt(t, err)

	for _, engine := range Engines {
		t.Run(string(engine), func(t *testing.T) {
			ctx := context.Background()

			in, err := Instantiate(ctx, engine, nil, bin, nil, nil)
			if err != nil {
				t.Fatal(err)
			}
			defer in.Close(ctx)

			for _, c := range sjljCases {
				before, err := in.Call(ctx, "probe_jumps")
				if err != nil {
					t.Fatal(err)
				}

				ftErr, err := in.Call(ctx, "probe_render", uint64(c.char), uint64(c.size))
				if err != nil {
					t.Fatalf("U+%04X at %d px: %v", c.char, c.size, err)
				}

				after, _ := in.Call(ctx, "probe_jumps")
				hash, _ := in.Call(ctx, "probe_hash")
				jumps := uint32(after) - uint32(before)

				t.Logf("U+%04X at %4d px: FT error %d, %2d longjmps, hash %08x", c.char, c.size, int32(ftErr), jumps, uint32(hash))

				if ftErr != 0 {
					t.Errorf("U+%04X at %d px: FreeType error %d", c.char, c.size, int32(ftErr))
				}

				if c.size >= 1000 && jumps == 0 {
					t.Errorf("U+%04X at %d px: no longjmp, so the overflow path was not exercised", c.char, c.size)
				}

				if uint32(hash) != c.hash {
					t.Errorf("U+%04X at %d px: bitmap hash %08x, native %08x", c.char, c.size, uint32(hash), c.hash)
				}
			}
		})
	}
}
