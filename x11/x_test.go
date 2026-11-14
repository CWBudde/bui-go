// SPDX-License-Identifier: GPL-2.0-or-later

package x11

import (
	"os"
	"os/exec"
	"strconv"
	"strings"
	"testing"
	"time"

	bui "github.com/CWBudde/bui-go"
)

// Real X input through xdotool, opt-in because it moves the pointer of the display it runs
// on, and changes its layout. TestMain gives the tests an Xvfb of their own when it can:
//
//	BUI_TEST_XDOTOOL=1 go test -race -count=1 -run '^TestX' ./x11/

// xdotool drives real X input for the TestX tests.
func xdotool(t *testing.T, args ...string) string {
	t.Helper()

	out, err := exec.Command("xdotool", args...).CombinedOutput()
	if err != nil {
		t.Errorf("xdotool %v: %v\n%s", args, err, out)
	}

	return strings.TrimSpace(string(out))
}

// xWindow opens a window holding one child that fills it, once input is opted in.
func xWindow(t *testing.T, typ, title string) (root, child *bui.Node, win *Window) {
	t.Helper()

	if os.Getenv("BUI_TEST_XDOTOOL") != "1" {
		t.Skip("opt-in with BUI_TEST_XDOTOOL=1, on a private display")
	}

	if !Supported() {
		t.Fatal("BUI_TEST_XDOTOOL=1 but no display")
	}

	root = bui.NewNode("frame")
	child = bui.NewNode(typ)
	root.Add(child)
	child.Pack(bui.PackOptions{Fill: bui.FillBoth, Expand: true})

	win, err := NewWindow(root, title, 300, 100)
	if err != nil {
		t.Fatal(err)
	}

	win.Show()

	return root, child, win
}

// xWhenLaidOut calls f on its own goroutine with n's rect and the window id, once the loop
// has rendered.
func xWhenLaidOut(t *testing.T, n *bui.Node, title string, f func(id string, x, y, w, h int)) {
	t.Helper()

	var try func()
	try = func() {
		x, y, w, h := n.Rect()

		if w <= 0 {
			go func() {
				time.Sleep(20 * time.Millisecond)
				bui.Do(try)
			}()

			return
		}
		go f(xdotool(t, "search", "--name", "^"+title+"$"), x, y, w, h)
	}
	whenInLoop(try)
}

func xClick(t *testing.T, n *bui.Node, title string) {
	t.Helper()

	xWhenLaidOut(t, n, title, func(id string, x, y, w, h int) {
		xdotool(t, "mousemove", "--window", id, strconv.Itoa(x+w/2), strconv.Itoa(y+h/2))
		time.Sleep(50 * time.Millisecond)
		xdotool(t, "click", "1")
	})
}

// A real click: the handler runs from an X event inside the loop and closes everything.
// Only the wake can then free it, and that alone must end Run.
func TestXClickClosesWindowAndRoot(t *testing.T) {
	const title = "bui-x11-close-all"

	root, btn, win := xWindow(t, "button", title)
	fired, looping := 0, false

	btn.OnClick(func() {
		fired++
		looping = std.looping

		win.Close()
		root.Close()
	})
	xClick(t, btn, title)
	runGuarded(t)

	if fired != 1 || !looping {
		t.Fatalf("fired=%d looping=%v, want 1 and true", fired, looping)
	}

	if !win.win.freed || !closed(root) {
		t.Fatalf("window freed=%v root closed=%v, want both", win.win.freed, closed(root))
	}
}

// A real click, the handler closes the window and the root follows in a Do.
func TestXClickWindowThenRootInDo(t *testing.T) {
	const title = "bui-x11-order"

	root, btn, win := xWindow(t, "button", title)
	windowGone := false

	btn.OnClick(func() {
		win.Close()
		bui.Do(func() {
			windowGone = win.win.freed

			root.Close()
		})
	})
	xClick(t, btn, title)
	runGuarded(t)

	if !windowGone || !closed(root) {
		t.Fatalf("window gone when root closed=%v, root closed=%v, want both", windowGone, closed(root))
	}
}

// A real click, the handler closes the button and quits: the last Flush's job.
func TestXClickClosesThenQuits(t *testing.T) {
	const title = "bui-x11-quit"

	root, btn, win := xWindow(t, "button", title)
	defer root.Close()
	defer win.Close()

	btn.OnClick(func() {
		btn.Close()
		Quit()
	})
	xClick(t, btn, title)
	runGuarded(t)

	if !closed(btn) {
		t.Fatal("button not freed by the time Run returned")
	}
}

// A real drag: the slider closes itself in its first OnChange with motion events still
// arriving and the pointer grabbed. It must stay silent, the loop must survive the grab's
// target going away, and later input must be harmless.
func TestXDragClosesSlider(t *testing.T) {
	const title = "bui-x11-drag"

	root, s, win := xWindow(t, "slider", title)
	defer root.Close()
	defer win.Close()

	if err := s.SetNum("max", 10); err != nil {
		t.Fatal(err)
	}

	fired := 0

	s.OnChange(func(float64) {
		fired++

		s.Close()
	})
	xWhenLaidOut(t, s, title, func(id string, x, y, _, h int) {
		cy := strconv.Itoa(y + h/2)
		xdotool(t, "mousemove", "--window", id, strconv.Itoa(x+20), cy, "mousedown", "1")

		for i := range 10 {
			xdotool(t, "mousemove", "--window", id, strconv.Itoa(x+40+i*20), cy)
		}

		xdotool(t, "mouseup", "1")
		xdotool(t, "mousemove", "--window", id, "5", "5", "click", "1")
		time.Sleep(50 * time.Millisecond)
		bui.Do(Quit)
	})
	runGuarded(t)

	if fired != 1 || !closed(s) {
		t.Fatalf("fired=%d closed=%v, want 1 and true", fired, closed(s))
	}
}

// Real key events, through the server's keymap: what the host's translation is for.
func xTypeInto(t *testing.T, title, text string) string {
	t.Helper()

	return xInputInto(t, title, func() {
		xdotool(t, "type", "--delay", "20", text)
	})
}

// xKeysInto presses keys by their names, as xdotool's "key" takes them.
func xKeysInto(t *testing.T, title string, keys []string) string {
	t.Helper()

	return xInputInto(t, title, func() {
		xdotool(t, append([]string{"key", "--delay", "20"}, keys...)...)
	})
}

// xInputInto clicks a text field, runs input on its own goroutine, and commits.
func xInputInto(t *testing.T, title string, input func()) string {
	t.Helper()

	root, text, win := xWindow(t, "text", title)

	var got string

	xWhenLaidOut(t, text, title, func(id string, x, y, w, h int) {
		xdotool(t, "mousemove", "--window", id, strconv.Itoa(x+w/2), strconv.Itoa(y+h/2))
		xdotool(t, "click", "1")
		time.Sleep(50 * time.Millisecond)

		input()

		xdotool(t, "key", "Return")
		time.Sleep(100 * time.Millisecond)
		bui.Do(func() {
			got = text.Str("text")

			Quit()
		})
	})

	runGuarded(t)
	win.Close()
	root.Close()

	return got
}

func TestXTypingASCII(t *testing.T) {
	if got := xTypeInto(t, "bui-x11 type", "Hello, W0rld!@#"); got != "Hello, W0rld!@#" {
		t.Errorf("typed %q", got)
	}
}

// Text beyond ASCII from real keys of a real layout, with Shift and AltGr. This host always
// gives UTF-8.
//
// The layout is set rather than left to xdotool, which for a key not on the layout remaps
// a spare keycode per character: a client reading the keymap can then race the next remap,
// and no real keyboard does that.
func TestXTypingOnAGermanLayout(t *testing.T) {
	if os.Getenv("BUI_TEST_XDOTOOL") != "1" {
		t.Skip("opt-in with BUI_TEST_XDOTOOL=1, on a private display")
	}

	if out, err := exec.Command("setxkbmap", "de", "-variant", "nodeadkeys").CombinedOutput(); err != nil {
		t.Skipf("setxkbmap: %v\n%s", err, out)
	}

	t.Cleanup(func() { _ = exec.Command("setxkbmap", "us").Run() })

	const want = "äÜß@€"

	if got := xKeysInto(t, "bui-x11 german", []string{
		"adiaeresis", "Udiaeresis", "ssharp", "ISO_Level3_Shift+q", "ISO_Level3_Shift+e",
	}); got != want {
		t.Errorf("typed %q, want %q", got, want)
	}
}

// The wheel, with a real Ctrl held, steps a slider.
func TestXWheelStepsASlider(t *testing.T) {
	const title = "bui-x11 wheel"

	root, slider, win := xWindow(t, "slider", title)
	defer root.Close()
	defer win.Close()

	for _, set := range []struct {
		key string
		v   float64
	}{{"min", 0}, {"max", 100}, {"value", 50}} {
		if err := slider.SetNum(set.key, set.v); err != nil {
			t.Fatal(err)
		}
	}

	var got float64

	xWhenLaidOut(t, slider, title, func(id string, x, y, w, h int) {
		xdotool(t, "mousemove", "--window", id, strconv.Itoa(x+w/2), strconv.Itoa(y+h/2))
		time.Sleep(50 * time.Millisecond)
		xdotool(t, "keydown", "ctrl", "click", "4", "keyup", "ctrl")
		time.Sleep(100 * time.Millisecond)
		bui.Do(func() {
			got = slider.Num("value")

			Quit()
		})
	})
	runGuarded(t)

	if got <= 50 {
		t.Errorf("the wheel left the value at %v", got)
	}
}
