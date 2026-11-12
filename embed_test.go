// SPDX-License-Identifier: GPL-2.0-or-later

package bui

import (
	"slices"
	"testing"
	"time"
)

// spyHost records what the kit asks of a program's window.
type spyHost struct {
	redraws   int
	cursors   []Cursor
	warps     [][2]int
	schedules []scheduled
	clip      string
	hasClip   bool
	onRedraw  func()
}

type scheduled struct {
	after time.Duration
	t     Timer
}

func (s *spyHost) Redraw() {
	s.redraws++

	if s.onRedraw != nil {
		s.onRedraw()
	}
}

func (s *spyHost) SetCursor(c Cursor)           { s.cursors = append(s.cursors, c) }
func (s *spyHost) Warp(x, y int)                { s.warps = append(s.warps, [2]int{x, y}) }
func (s *spyHost) ClipboardGet() (string, bool) { return s.clip, s.hasClip }
func (s *spyHost) ClipboardSet(text string)     { s.clip, s.hasClip = text, true }
func (s *spyHost) Schedule(after time.Duration, t Timer) {
	s.schedules = append(s.schedules, scheduled{after, t})
}

func center(n *Node) (x, y int) {
	x, y, w, h := n.Rect()

	return x + w/2, y + h/2
}

func TestHeadlessHostRedraw(t *testing.T) {
	_, btn, h := buttonInFrame(t)
	spy := &spyHost{}
	h.SetHost(spy)

	x, y := center(btn)
	h.Motion(x, y, 0)

	if spy.redraws == 0 {
		t.Error("hovering a button asked for no redraw")
	}
}

func TestHeadlessHostCursor(t *testing.T) {
	txt, h := textInFrame(t, "")
	spy := &spyHost{}
	h.SetHost(spy)

	x, y := center(txt)
	h.Motion(x, y, 0)

	if !slices.Contains(spy.cursors, CursorText) {
		t.Errorf("cursors over a text field: %v, want %d among them", spy.cursors, CursorText)
	}

	h.Motion(-100000, -100000, 0)

	if len(spy.cursors) == 0 || spy.cursors[len(spy.cursors)-1] != CursorDefault {
		t.Errorf("cursors after leaving: %v, want the last to be %d", spy.cursors, CursorDefault)
	}
}

// Hovering a button with a description schedules the tooltip, and firing the timer draws
// it. Firing it again changes nothing, so a host can fire every timer it was given.
func TestHeadlessHostTooltip(t *testing.T) {
	_, btn, h := buttonInFrame(t)

	if err := btn.SetStr("description", "A tip"); err != nil {
		t.Fatal(err)
	}

	spy := &spyHost{}
	h.SetHost(spy)
	h.Render()

	x, y := center(btn)
	h.Motion(x, y, 0)
	h.RenderDamage(false)

	if len(spy.schedules) != 1 {
		t.Fatalf("schedules after hovering: %v, want one", spy.schedules)
	}

	if after := spy.schedules[0].after; after <= 0 || after > 5*time.Second {
		t.Errorf("tooltip delay = %v, want a positive delay of at most 5 s", after)
	}

	h.Timer(spy.schedules[0].t)

	if len(h.RenderDamage(false)) == 0 {
		t.Error("the tooltip timer drew nothing")
	}

	h.Timer(spy.schedules[0].t)

	if damage := h.RenderDamage(false); len(damage) != 0 {
		t.Errorf("firing the tooltip timer again redrew %v", damage)
	}
}

// A number field wraps the pointer at the raster's edges while it is dragged.
func TestHeadlessHostWarp(t *testing.T) {
	root := NewNode("frame")
	num := NewNode("number")

	for _, kv := range []struct {
		key string
		v   float64
	}{{"min", -1000}, {"max", 1000}, {"value", 0}} {
		if err := num.SetNum(kv.key, kv.v); err != nil {
			t.Fatal(err)
		}
	}

	root.Add(num)
	num.Pack(PackOptions{Fill: FillBoth, Expand: true})
	t.Cleanup(root.Close)

	h := NewHeadless(root, 300, 120)
	t.Cleanup(h.Close)

	spy := &spyHost{}
	h.SetHost(spy)
	h.Render()

	x, y := center(num)
	h.Motion(x, y, 0)
	h.PressMods(x, y, 0)

	for dx := 20; dx < 300; dx += 20 {
		h.Motion(x+dx, y, ModButton1)
	}

	h.ReleaseMods(300, y, 0)

	if len(spy.warps) == 0 {
		t.Errorf("no warp from dragging a number field to the edge, value %v", num.Num("value"))
	}
}

func TestHeadlessHostClipboard(t *testing.T) {
	txt, h := textInFrame(t, "copied")
	spy := &spyHost{}
	h.SetHost(spy)

	x, y := center(txt)
	h.Press(x, y)
	h.Release(x, y)
	h.Key("", "a", ModCtrl)
	h.Key("", "c", ModCtrl)

	if spy.clip != "copied" {
		t.Errorf("clipboard after Ctrl+C = %q, want %q", spy.clip, "copied")
	}

	spy.clip = "pasted ä"

	h.Key("", "a", ModCtrl)
	h.Key("", "v", ModCtrl)
	h.Key("", "Return", 0)

	if got := txt.Str("text"); got != "pasted ä" {
		t.Errorf("text after Ctrl+V = %q, want %q", got, "pasted ä")
	}
}

func TestHeadlessSetHostNil(t *testing.T) {
	txt, h := textInFrame(t, "")
	spy := &spyHost{}
	h.SetHost(spy)
	h.SetHost(nil)

	x, y := center(txt)
	h.Motion(x, y, 0)

	if spy.redraws != 0 || len(spy.cursors) != 0 {
		t.Errorf("after SetHost(nil) the host got %d redraws and cursors %v", spy.redraws, spy.cursors)
	}
}

// Close from inside a Host method waits until the kit call that made it returns, as from
// a handler: freeing the context under BK_motion would be use-after-free.
func TestHeadlessCloseFromHost(t *testing.T) {
	_, btn, h := buttonInFrame(t)

	freedInside := false
	spy := &spyHost{}
	spy.onRedraw = func() {
		spy.onRedraw = nil

		h.Close()
		freedInside = h.r == 0
	}
	h.SetHost(spy)

	x, y := center(btn)
	h.Motion(x, y, 0)

	if freedInside {
		t.Error("Close from a Host method freed the Headless inside the kit call")
	}

	if h.r != 0 {
		t.Error("the Headless was not freed after the kit call returned")
	}
}

func TestHeadlessAnimating(t *testing.T) {
	_, _, h := buttonInFrame(t)

	if h.Animating() {
		t.Error("a still button is animating")
	}
}

// A program with its own loop hears of Do through SetWake and runs it with Flush.
func TestFlushRunsDo(t *testing.T) {
	woke := make(chan struct{}, 1)

	SetWake(func() {
		select {
		case woke <- struct{}{}:
		default:
		}
	})
	t.Cleanup(func() { SetWake(nil) })

	ran := false

	go Do(func() { ran = true })

	select {
	case <-woke:
	case <-time.After(10 * time.Second):
		t.Fatal("Do from another goroutine did not call the wake function")
	}

	if ran {
		t.Fatal("Do ran before Flush")
	}

	Flush()

	if !ran {
		t.Error("Flush did not run the queued function")
	}
}

func TestSetWakeNil(t *testing.T) {
	called := false

	SetWake(func() { called = true })
	SetWake(nil)
	Do(func() {})
	Flush()

	if called {
		t.Error("SetWake(nil) left the wake function in place")
	}
}

// Flush from a handler would free what the kit is delivering the event for.
func TestFlushFromHandlerPanics(t *testing.T) {
	_, btn, h := buttonInFrame(t)

	clicked := false

	btn.OnClick(func() {
		clicked = true

		mustPanic(t, "Flush from a handler", Flush)
	})

	x, y := center(btn)
	h.Press(x, y)
	h.Release(x, y)

	if !clicked {
		t.Fatal("the click did not reach the handler")
	}
}

func TestHeadlessHostAfterClosePanics(t *testing.T) {
	_, _, h := buttonInFrame(t)
	h.Close()

	for name, f := range map[string]func(){
		"SetHost":   func() { h.SetHost(&spyHost{}) },
		"Timer":     func() { h.Timer(0) },
		"Animating": func() { h.Animating() },
	} {
		mustPanic(t, "Headless."+name, f)
	}
}
