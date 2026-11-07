// SPDX-License-Identifier: GPL-2.0-or-later

package wasmengine

import (
	"bytes"
	"context"
	"image/png"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/CWBudde/bui-go/internal/engine"
	"github.com/tetratelabs/wazero"
	"github.com/tetratelabs/wazero/api"
	"github.com/tetratelabs/wazero/experimental"
)

// callCounts counts calls of the named functions. The module's name section finds them
// although they are not exported.
type callCounts map[string]int

func (c callCounts) NewFunctionListener(def api.FunctionDefinition) experimental.FunctionListener {
	name := def.Name()
	if _, ok := c[name]; !ok {
		return nil
	}

	return experimental.FunctionListenerFunc(func(context.Context, api.Module, api.FunctionDefinition, []uint64,
		experimental.StackIterator,
	) {
		c[name]++
	})
}

// TestImageStaysPendingWithoutThreads shows the premise of the image decision in
// docs/design.md. The
// loader decodes only on worker threads, wasip1 cannot create them, and
// BLI_threadpool_insert ignores the failure, so a request queues forever. The file is
// readable, so the cause is the threads and not the file system. The native kit
// decodes the same file: see TestImageDecodesNatively in internal/enginetest/cgoimage.
//
// The module exports nothing to ask about this. Layout reads the file's header on this
// thread, which shows that the file is readable, and the loader's own functions show
// what happens to the request. It is pending if it was pushed once, never cancelled, and
// its decode never arrived. Only the pixels can tell the last part: BK_render polls too,
// so a poll here may find nothing even after a decode.
func TestImageStaysPendingWithoutThreads(t *testing.T) {
	const (
		dir         = "../../../third_party/BUI/tests/images"
		file        = "quad.png"
		w, h        = 64, 64
		fitFill     = 0 // BK_FIT_FILL
		renderFor   = time.Second
		renderPause = 10 * time.Millisecond
	)

	b, err := os.ReadFile(filepath.Join(dir, file))
	if err != nil {
		t.Skipf("skipping: BUI's test images are missing (%v); run scripts/build-wasm.sh", err)
	}

	cfg, err := png.DecodeConfig(bytes.NewReader(b))
	if err != nil {
		t.Fatal(err)
	}

	for _, tc := range []struct {
		name        string
		interpreter bool
	}{{"compiler", false}, {"interpreter", true}} {
		t.Run(tc.name, func(t *testing.T) {
			calls := callCounts{"bk_loader_push": 0, "bk_loader_cancel": 0}

			m, err := instantiate(experimental.WithFunctionListenerFactory(context.Background(), calls),
				kitModule, options{
					interpreter:      tc.interpreter,
					memoryLimitPages: memoryLimitPages,
					fs:               wazero.NewFSConfig().WithReadOnlyDirMount(dir, "/img"),
				}, nil)
			if err != nil {
				t.Fatal(err)
			}

			t.Cleanup(m.close)

			k := Kit{m: m}

			root := newNode(t, k, "frame", 0)
			bare := newNode(t, k, "image", 0)
			img := newNode(t, k, "image", 0)
			packImage(t, k, root, bare)
			packImage(t, k, root, img)
			m.call("BK_node_set_image_source", ptr(img), api.EncodeU32(m.cstr("/img/"+file)), fitFill)

			ctx := k.ContextNew(root)
			r := k.RasterNew(w, h)

			t.Cleanup(func() {
				k.RasterFree(r)
				k.ContextFree(ctx)
			})

			k.Render(ctx, r, w, h)
			first := k.RasterPixels(r)

			checkImageRead(t, k, bare, img, cfg.Width, cfg.Height)

			polled := 0

			for deadline := time.Now().Add(renderFor); time.Now().Before(deadline); time.Sleep(renderPause) {
				polled += k.ImagePoll()

				k.Render(ctx, r, w, h)

				if !slices.Equal(k.RasterPixels(r), first) {
					t.Fatal("the image was drawn: a decode arrived without worker threads")
				}
			}

			pushed, cancelled := calls["bk_loader_push"], calls["bk_loader_cancel"]
			t.Logf("after %v: %d pushed, %d cancelled, %d handed over", renderFor, pushed, cancelled, polled)

			if pushed != 1 || cancelled != 0 || polled != 0 {
				t.Errorf("want one request staying queued, got %d pushed, %d cancelled, %d handed over",
					pushed, cancelled, polled)
			}
		})
	}
}

// packImage packs without filling, so that layout gives the node its image's size.
func packImage(t *testing.T, k Kit, root, img engine.Node) {
	t.Helper()

	addPacked(t, k, root, img)
	k.GeomSet(img, "fill", 0)
}

// checkImageRead requires that the file's header was read through WASI: img measures as
// the file says, and a node without a source does not.
func checkImageRead(t *testing.T, k Kit, bare, img engine.Node, wantW, wantH int) {
	t.Helper()

	_, _, bareW, bareH := k.NodeRect(bare)
	_, _, imgW, imgH := k.NodeRect(img)

	t.Logf("the node measures %dx%d, without a source %dx%d", imgW, imgH, bareW, bareH)

	if imgW != wantW || imgH != wantH {
		t.Errorf("the node measures %dx%d, the file says %dx%d: it is not readable through WASI",
			imgW, imgH, wantW, wantH)
	}

	if bareW == wantW && bareH == wantH {
		t.Errorf("a node without a source also measures %dx%d, so its size shows nothing", bareW, bareH)
	}
}
