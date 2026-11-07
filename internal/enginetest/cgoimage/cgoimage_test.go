// SPDX-License-Identifier: GPL-2.0-or-later

//go:build cgo && linux && amd64

package cgoimage

import (
	"os"
	"slices"
	"testing"
	"time"

	"github.com/CWBudde/bui-go/internal/engine/cgoengine"
)

// TestImageDecodesNatively is the control for TestImageStaysPendingWithoutThreads in
// wasmengine: with worker threads the same file and render loop reach the screen, and
// layout gives the node the size that test relies on.
func TestImageDecodesNatively(t *testing.T) {
	const (
		file         = "../../../third_party/BUI/tests/images/quad.png"
		fileW, fileH = 8, 6
		w, h         = 64, 64
		timeout      = 5 * time.Second
		pause        = 10 * time.Millisecond
	)

	if _, err := os.Stat(file); err != nil {
		t.Skipf("skipping: BUI's test images are missing (%v); run scripts/build-wasm.sh", err)
	}

	k := cgoengine.Kit{}

	root, img := k.NodeNew("frame"), k.NodeNew("image")
	defer k.NodeFree(root)

	if !k.NodeAdd(root, img) {
		t.Fatal("NodeAdd refused the image")
	}

	k.GeomSetPack(img)
	SetSource(img, file)

	ctx := k.ContextNew(root)
	r := k.RasterNew(w, h)

	defer func() {
		k.RasterFree(r)
		k.ContextFree(ctx)
	}()

	k.Render(ctx, r, w, h)
	first := k.RasterPixels(r)

	if _, _, gotW, gotH := k.NodeRect(img); gotW != fileW || gotH != fileH {
		t.Errorf("the node measures %dx%d, the file is %dx%d", gotW, gotH, fileW, fileH)
	}

	for start := time.Now(); time.Since(start) < timeout; time.Sleep(pause) {
		k.ImagePoll()
		k.Render(ctx, r, w, h)

		if !slices.Equal(k.RasterPixels(r), first) {
			t.Logf("drawn after %v", time.Since(start))

			return
		}
	}

	t.Errorf("the image was not drawn within %v", timeout)
}
