// SPDX-License-Identifier: GPL-2.0-or-later

package wasmspike

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/tetratelabs/wazero"
)

// buiTestImages is BUI's own image test directory, fetched by the build scripts.
func buiTestImages() string {
	_, file, _, _ := runtime.Caller(0) //nolint:dogsled // only the file is needed

	return filepath.Join(filepath.Dir(file), "..", "..", "third_party", "BUI", "tests", "images")
}

// waitForImage renders and polls for up to d, as a window loop would, and
// returns the pending count at the end and how many decodes were handed over.
func waitForImage(k Kit, hl Headless, d time.Duration) (pending, polled int) {
	deadline := time.Now().Add(d)
	for time.Now().Before(deadline) {
		polled += k.ImagePoll()

		hl.Render()

		if k.ImagePending() == 0 {
			break
		}

		time.Sleep(10 * time.Millisecond)
	}

	return k.ImagePending(), polled
}

func imageTree(k Kit, src string) Node {
	root := k.NewNode("frame")
	img := k.NewNode("image")
	k.Add(root, img)
	k.Pack(img, PackOptions{Fill: fillBoth, Expand: true})
	k.SetImageSource(img, src)

	return root
}

// TestImageStaysPendingWithoutThreads shows the image decision's premise: the
// loader decodes only on worker threads, wasip1 cannot create them, and
// BLI_threadpool_insert ignores the failure, so a request queues forever. The
// file is readable (mounted), so the cause is the threads, not the file
// system. The native kit decodes the same file.
func TestImageStaysPendingWithoutThreads(t *testing.T) {
	bin, err := ReadModule("bui_kit.wasm")
	skipIfNotBuilt(t, err)

	dir := buiTestImages()
	if _, err := os.Stat(filepath.Join(dir, "quad.png")); err != nil {
		t.Skipf("skipping: BUI's test images are missing (%v); run scripts/build-wasm.sh", err)
	}

	if NativeAvailable {
		t.Run("native", func(t *testing.T) {
			k := NewNativeKit()
			defer k.Close()

			root := imageTree(k, filepath.Join(dir, "quad.png"))
			hl := k.NewHeadless(root, 64, 64)

			defer func() {
				hl.Close()
				k.FreeTree(root)
			}()

			hl.Render()

			pending, polled := waitForImage(k, hl, 5*time.Second)
			t.Logf("native: pending %d, %d decodes handed over", pending, polled)

			if pending != 0 || polled == 0 {
				t.Errorf("native: the image did not decode (pending %d, polled %d)", pending, polled)
			}
		})
	}

	for _, engine := range Engines {
		t.Run("wasm-"+string(engine), func(t *testing.T) {
			fs := wazero.NewFSConfig().WithReadOnlyDirMount(dir, "/img")

			k, err := NewWasmKit(context.Background(), engine, nil, bin, ModuleConfig().WithFSConfig(fs))
			if err != nil {
				t.Fatal(err)
			}
			defer k.Close()

			src := k.cstr("/img/quad.png")
			width := int32(k.Call("bui_image_info_width", uint64(src)))
			k.free(src)

			if width <= 0 {
				t.Fatalf("the mounted file is not readable through WASI (BK_image_info: %d)", width)
			}

			t.Logf("wasm-%s: BK_image_info reads the header synchronously: width %d", engine, width)

			root := imageTree(k, "/img/quad.png")
			hl := k.NewHeadless(root, 64, 64)

			defer func() {
				hl.Close()
				k.FreeTree(root)
			}()

			hl.Render()

			pending, polled := waitForImage(k, hl, time.Second)
			t.Logf("wasm-%s: still pending %d after 1 s, %d decodes handed over", engine, pending, polled)

			if pending != 1 || polled != 0 {
				t.Errorf("expected the request to stay pending without workers, got pending %d, polled %d", pending, polled)
			}
		})
	}
}
