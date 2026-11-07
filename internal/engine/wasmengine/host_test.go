// SPDX-License-Identifier: GPL-2.0-or-later

package wasmengine

import (
	"path/filepath"
	"sync"
	"testing"

	"github.com/CWBudde/bui-go/internal/engine"
)

// resetLoaded makes the process's module not loaded for this test, and puts the
// user's cache directory somewhere temporary in case it does load.
func resetLoaded(t *testing.T) {
	t.Helper()

	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CACHE_HOME", filepath.Join(home, "cache"))
	t.Setenv("LocalAppData", filepath.Join(home, "cache"))

	reset := func() {
		if loaded != nil {
			loaded.close()
		}

		loadOnce = sync.Once{}
		loaded = nil
		loadedEmit = nil
	}

	reset()
	t.Cleanup(reset)
}

func TestHostHasNoWindowsAndNeverLoadsTheModule(t *testing.T) {
	resetLoaded(t)

	var h engine.Host = Host{}

	if h.Supported() {
		t.Error("Supported is true")
	}

	if win := h.WindowNew(1, "title", 100, 100); win != 0 {
		t.Errorf("WindowNew = %#x, want 0", win)
	}

	h.WindowShow(0)
	h.WindowFree(0)
	h.WakeInit(func() { t.Error("the wake function ran") })
	h.Wake()

	if err := h.Run(); err != nil {
		t.Errorf("Run = %v", err)
	}

	h.Quit()

	// Wake is the call the seam allows from any goroutine, and Quit is what bui
	// must be able to reach through Do. -race would catch either touching state.
	var wg sync.WaitGroup

	for range 8 {
		wg.Go(func() {
			for range 100 {
				h.Wake()
				h.Quit()
			}
		})
	}

	wg.Wait()

	if loaded != nil {
		t.Fatal("Host loaded the module")
	}
}

func TestSetEmitOnTheZeroKitDoesNotLoadTheModule(t *testing.T) {
	resetLoaded(t)

	var first, second int

	k := Kit{}
	k.SetEmit(func(uint32, int, float64) { first++ })

	if loaded != nil {
		t.Fatal("SetEmit loaded the module")
	}

	// Set before the load, so the load must apply it.
	ctx, _, x, y := clickableButton(t, k)
	clickAt(k, ctx, x, y)

	if loaded == nil {
		t.Fatal("a kit call did not load the module")
	}

	if first != 1 {
		t.Fatalf("the receiver set before the load saw %d events, want 1", first)
	}

	// And set after the load it must take effect at once.
	k.SetEmit(func(uint32, int, float64) { second++ })
	clickAt(k, ctx, x, y)

	if first != 1 || second != 1 {
		t.Fatalf("after replacing the receiver: first %d, second %d", first, second)
	}
}
