// SPDX-License-Identifier: GPL-2.0-or-later

package x11

import (
	"image"
	"os/exec"
	"strings"
	"testing"
	"time"

	bui "github.com/CWBudde/bui-go"
	"github.com/jezek/xgb/xproto"
)

// The package builds on package bui's public API only, so that it can live in a module of
// its own that requires the binding.
func TestImportsOnlyThePublicAPI(t *testing.T) {
	out, err := exec.Command("go", "list", "-f", `{{join .Imports "\n"}}`, ".").CombinedOutput()
	if err != nil {
		t.Skipf("go list: %v\n%s", err, out)
	}

	for imp := range strings.SplitSeq(strings.TrimSpace(string(out)), "\n") {
		if strings.HasPrefix(imp, "github.com/CWBudde/bui-go/") && !strings.HasPrefix(imp, "github.com/CWBudde/bui-go/x11/") {
			t.Errorf("imports %s", imp)
		}
	}
}

// A handler closes the window while a render and a tooltip are pending. The loop must
// leave the closed Headless alone, which would panic, and end.
func TestCloseFromAHandlerWithWorkPending(t *testing.T) {
	f := newFixture(t, "button", 300, 200)
	mustDo(t, f.child.SetStr("text", "Close me"))
	mustDo(t, f.child.SetStr("description", "A tooltip"))
	f.show()

	f.child.OnClick(func() { f.w.free() })

	x, y := f.center()
	f.motion(x, y, 0)

	if f.w.tipDue.IsZero() {
		t.Fatal("hovering scheduled no tooltip")
	}

	f.press(buttonLeft, x, y, 0)
	f.release(buttonLeft, x, y, xproto.KeyButMaskButton1)

	if !f.w.freed || f.h.open != 0 {
		t.Fatalf("freed = %v, open = %d", f.w.freed, f.h.open)
	}

	f.w.renderPending = true
	f.w.tipDue = f.w.tipDue.Add(-10 * time.Second)
	f.h.Wake()
	f.h.turn()

	if err := runWithTimeout(t, f.h); err != nil {
		t.Errorf("Run = %v", err)
	}
}

// Windows of different scales share the process, each drawing at its own, as the kit's
// scale is process-wide.
func TestWindowsOfDifferentScales(t *testing.T) {
	h := testHost(t)

	open := func(scale float32) (*window, *bui.Node) {
		h.scale = scale

		root, button := bui.NewNode("frame"), bui.NewNode("button")
		mustDo(t, button.SetStr("text", "Scaled"))
		root.Add(button)
		button.Pack(bui.PackOptions{})

		win, err := h.newWindow(root, "bui x11 scale", 200, 100)
		if err != nil {
			t.Fatal(err)
		}

		t.Cleanup(func() {
			win.free()
			root.Close()
		})

		win.show()
		win.render()

		return win, button
	}

	one, small := open(1)
	_, large := open(2)

	h1, h2 := height(small), height(large)

	if h1 <= 0 || h2 != 2*h1 {
		t.Errorf("button heights %d and %d, want the second twice the first", h1, h2)
	}

	// The first window again, after the second drew at 2: a full layout at its own scale.
	one.hl.SetScale(1.5)
	one.hl.SetScale(1)
	one.full = true
	one.render()

	if again := height(small); again != h1 {
		t.Errorf("the first button is %d high after the second window drew, want %d", again, h1)
	}
}

func height(n *bui.Node) int {
	x, y, w, h := n.Rect()

	return image.Rect(x, y, x+w, y+h).Dy()
}
