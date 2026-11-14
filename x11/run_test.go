// SPDX-License-Identifier: GPL-2.0-or-later

package x11

import (
	"fmt"
	"os"
	"testing"
	"time"

	bui "github.com/CWBudde/bui-go"
)

// These run the public API on the process's host, as a program does: package bui's
// window tests, which on Linux without cgo now skip there.

// runGuarded runs the loop, ending the process if it never returns.
func runGuarded(t *testing.T) {
	t.Helper()

	name := t.Name()

	guard := time.AfterFunc(10*time.Second, func() {
		fmt.Fprintf(os.Stderr, "FAIL: %s: Run did not return within 10s (lost wake or quit)\n", name)
		os.Exit(3)
	})
	defer guard.Stop()

	if err := Run(); err != nil {
		t.Fatalf("Run: %v", err)
	}
}

func labelWindow(t *testing.T) (root, label *bui.Node, win *Window) {
	t.Helper()

	if !Supported() {
		t.Skip("no display")
	}

	root = bui.NewNode("frame")

	label = bui.NewNode("label")
	if err := label.SetStr("text", "before"); err != nil {
		t.Fatal(err)
	}

	root.Add(label)
	label.Pack(bui.PackOptions{Fill: bui.FillX})

	win, err := NewWindow(root, "bui-go x11 test", 320, 120)
	if err != nil {
		t.Fatal(err)
	}

	win.Show()

	return root, label, win
}

// whenInLoop runs f from a wake inside the loop. A function that finds itself in Run's
// first Flush queues itself again, so the path is certain rather than a matter of timing.
func whenInLoop(f func()) {
	bui.Do(func() {
		if std.looping {
			f()
		} else {
			whenInLoop(f)
		}
	})
}

func mustPanic(t *testing.T, what string, f func()) {
	t.Helper()

	defer func() {
		if recover() == nil {
			t.Errorf("%s did not panic", what)
		}
	}()

	f()
}

// closed reports whether n is closed: a closed node panics on use.
func closed(n *bui.Node) (yes bool) {
	defer func() { yes = recover() != nil }()

	n.Str("text")

	return false
}

// The full path: a goroutine reaches the UI thread through bui.Do, the wake runs it inside
// the loop, Quit ends the loop, and shutdown runs window, then root.
func TestWindowDoAndQuit(t *testing.T) {
	root, label, win := labelWindow(t)

	go func() {
		whenInLoop(func() {
			if err := label.SetStr("text", "from goroutine"); err != nil {
				t.Error(err)
			}

			bui.Do(Quit)
		})
	}()

	runGuarded(t)

	if got := label.Str("text"); got != "from goroutine" {
		t.Fatalf("label = %q after Run", got)
	}

	mustPanic(t, "root.Close before Window.Close", root.Close)
	win.Close()
	win.Close()
	mustPanic(t, "Window.Show after Close", win.Show)
	root.Close()
}

// Queued before Run, a function and a Quit must still take effect.
func TestWindowDoQueuedBeforeRun(t *testing.T) {
	root, label, win := labelWindow(t)
	defer root.Close()
	defer win.Close()

	ran := 0

	bui.Do(func() {
		ran++

		if err := label.SetStr("text", "queued"); err != nil {
			t.Error(err)
		}
	})
	bui.Do(Quit)
	runGuarded(t)

	if ran != 1 || label.Str("text") != "queued" {
		t.Fatalf("ran=%d label=%q, want the queued function to have run once", ran, label.Str("text"))
	}
}

// The loop can run again after a Quit, and a burst of wakes loses nothing.
func TestWindowRunTwiceAndWakeBurst(t *testing.T) {
	root, _, win := labelWindow(t)
	defer root.Close()
	defer win.Close()

	for range 2 {
		const burst = 2000

		count := 0

		go func() {
			for range burst {
				bui.Do(func() { count++ })
			}

			bui.Do(Quit)
		}()

		runGuarded(t)

		if count != burst {
			t.Fatalf("ran %d of %d functions", count, burst)
		}
	}
}

// A handler inside the loop closes the window and the root, and the loop must end by
// itself, as its only window is gone. That the frees wait until the kit is off the stack
// is package bui's to test: a closed node refuses use either way.
func TestWindowCloseInsideCallback(t *testing.T) {
	if !Supported() {
		t.Skip("no display")
	}

	root := bui.NewNode("frame")

	s := bui.NewNode("slider")
	if err := s.SetNum("max", 10); err != nil {
		t.Fatal(err)
	}

	root.Add(s)
	s.Pack(bui.PackOptions{Fill: bui.FillX})

	win, err := NewWindow(root, "bui-go x11 test", 320, 120)
	if err != nil {
		t.Fatal(err)
	}

	win.Show()

	fired := 0

	s.OnChange(func(float64) {
		fired++

		if !std.looping {
			t.Error("handler ran outside the loop: the test would prove nothing")
		}

		win.Close()
		root.Close()
	})
	whenInLoop(func() {
		if err := s.SetNum("set_value", 5); err != nil {
			t.Error(err)
		}
	})
	runGuarded(t)

	if fired != 1 {
		t.Fatalf("fired=%d, want 1", fired)
	}

	if !closed(root) {
		t.Fatal("root not closed after Run returned")
	}
}

// A handler that closes something and quits: the loop ends with the wake unread, so only
// Run's last Flush can run the free and the Do, in that order.
func TestHandlerClosesThenQuits(t *testing.T) {
	root, label, win := labelWindow(t)
	defer root.Close()
	defer win.Close()

	button := bui.NewNode("button")
	root.Add(button)
	button.Pack(bui.PackOptions{Fill: bui.FillX})

	ran, closedInDo := false, false

	button.OnClick(func() {
		label.Close()
		bui.Do(func() {
			ran = true
			closedInDo = closed(label)
		})
		Quit()
	})
	whenInLoop(func() { clickNode(win.win, button) })
	runGuarded(t)

	if !closed(label) {
		t.Fatal("label not freed by the time Run returned")
	}

	if !ran || !closedInDo {
		t.Fatalf("Do ran=%v and saw the free done=%v, want both", ran, closedInDo)
	}
}

// A panic in a function bui.Do queued propagates from Run, which can then run again with
// the functions behind it.
func TestPanicInDoPropagatesFromRun(t *testing.T) {
	root, _, win := labelWindow(t)
	defer root.Close()
	defer win.Close()

	ran := false

	whenInLoop(func() {
		bui.Do(func() { ran = true })
		panic("boom")
	})

	func() {
		defer func() {
			if r := recover(); r != "boom" {
				t.Errorf("recovered %v, want boom", r)
			}
		}()

		_ = Run()

		t.Error("Run returned")
	}()

	if ran {
		t.Error("the function behind the panic ran")
	}

	bui.Do(Quit)
	runGuarded(t)

	if !ran {
		t.Error("the function behind the panic did not run in the next Run")
	}
}
