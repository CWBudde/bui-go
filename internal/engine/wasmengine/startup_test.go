// SPDX-License-Identifier: GPL-2.0-or-later

package wasmengine

import (
	"context"
	"strconv"
	"testing"

	"github.com/tetratelabs/wazero/experimental"
)

// BenchmarkStartup measures the lazy module load: a new runtime, compile, instantiate
// and _initialize. "cold" has no cache. "cached" opens a new cache over a populated
// directory per iteration, as a new process does: one cache instance would also share
// compiled code in memory.
func BenchmarkStartup(b *testing.B) {
	for _, tc := range []struct {
		name        string
		interpreter bool
	}{{"compiler", false}, {"interpreter", true}} {
		o := options{interpreter: tc.interpreter, memoryLimitPages: memoryLimitPages}

		for _, workers := range []int{1, 4} {
			b.Run(tc.name+"/cold/workers="+strconv.Itoa(workers), func(b *testing.B) {
				ctx := experimental.WithCompilationWorkers(context.Background(), workers)

				for b.Loop() {
					m, err := instantiate(ctx, kitModule, o, nil)
					if err != nil {
						b.Fatal(err)
					}

					m.close()
				}
			})
		}

		b.Run(tc.name+"/cached", func(b *testing.B) {
			cached := o
			cached.cacheDir = b.TempDir()

			// The first load fills the directory, and close drops what it holds in memory.
			warm, err := load(kitModule, cached)
			if err != nil {
				b.Fatal(err)
			}

			warm.close()

			for b.Loop() {
				m, err := load(kitModule, cached)
				if err != nil {
					b.Fatal(err)
				}

				m.close()
			}
		})
	}
}
