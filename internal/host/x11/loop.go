// SPDX-License-Identifier: GPL-2.0-or-later

package x11

import (
	"fmt"
	"os"
	"time"

	"github.com/CWBudde/bui-go/internal/engine"
	"github.com/jezek/xgb"
	"github.com/jezek/xgb/xproto"
)

// framePeriod paces frames while something animates, as the C host's refresh_ms does.
const framePeriod = time.Second / 60

// Run implements engine.Host. As in the C host it erases an earlier Quit, and returns
// right away with no window open.
func (h *Host) Run() {
	if h.c == nil {
		return
	}

	h.quit = false

	for h.open > 0 && !h.quit {
		h.turn()
	}
}

// turn is one pass of the loop: wait, then events, timers and renders.
func (h *Host) turn() {
	h.wait()
	h.checkConnection()

	for {
		ev := h.events.pop()
		if ev == nil {
			break
		}

		h.handle(ev)
	}

	now := time.Now()

	for _, win := range h.windowsSnapshot() {
		if win.xid != 0 {
			win.runTimers(now)
		}
	}

	h.renderPending(now)
}

// windowsSnapshot is what a pass goes over: a kit call runs handlers, and what they do
// must not move the list under the iteration.
func (h *Host) windowsSnapshot() []*window {
	return append([]*window(nil), h.windows...)
}

// wait blocks until there is something to do, in one select over X events, timers,
// frames and the wake channel. A pending wake runs whether or not this blocks: the C
// host only reads its pipe when it would block, so an animation starves Do.
func (h *Host) wait() {
	wait, forever := h.nextWait(time.Now())

	if !h.events.empty() {
		wait, forever = 0, false
	}

	if !forever && wait <= 0 {
		select {
		case <-h.wake:
			h.drainWake()
		default:
		}

		return
	}

	var timeout <-chan time.Time

	if !forever {
		t := time.NewTimer(wait)
		defer t.Stop()

		timeout = t.C
	}

	select {
	case <-h.wake:
		h.drainWake()
	case <-h.events.notify:
	case <-timeout:
	}
}

// nextWait returns how long nothing is due, or forever.
func (h *Host) nextWait(now time.Time) (wait time.Duration, forever bool) {
	forever = true

	note := func(d time.Duration) {
		if forever || d < wait {
			wait, forever = max(d, 0), false
		}
	}

	for _, win := range h.windows {
		if win.xid == 0 {
			continue
		}

		if win.renderPending {
			note(h.frameDue.Sub(now))
		}

		if !win.tipDue.IsZero() {
			note(win.tipDue.Sub(now))
		}

		if !win.customDue.IsZero() {
			note(win.customDue.Sub(now))
		}
	}

	return wait, forever
}

// renderPending renders what asked for it, unless the last frame's animation has not
// had its time yet. Unlike the C host's sleep, events and wakes run in the meantime.
func (h *Host) renderPending(now time.Time) {
	if now.Before(h.frameDue) {
		return
	}

	animating := false

	for _, win := range h.windowsSnapshot() {
		if win.xid != 0 && win.renderPending {
			win.render()

			animating = animating || win.renderPending
		}
	}

	if animating {
		h.frameDue = now.Add(framePeriod)
	}
}

// drainWake is the wake's work: decoded images, then the wake function. It is guarded
// like a callback from the kit, as this package's loop stands for the kit's: a panic in
// a Do function running in the loop ends the process, as package bui documents.
func (h *Host) drainWake() {
	returned := false

	defer engine.ExitIfUnwinding(&returned)

	h.kit.ImagePoll()

	if h.wakeFunc != nil {
		h.wakeFunc()
	}

	returned = true
}

// checkConnection ends the process when the server is gone, as Xlib's default handler
// does: nothing the host does can work any more.
func (h *Host) checkConnection() {
	if h.events.isClosed() {
		fmt.Fprintln(os.Stderr, "bui: the X connection is broken")
		os.Exit(1)
	}
}

// handle runs one event.
//
//nolint:cyclop // one short case per event type
func (h *Host) handle(ev xgb.Event) {
	switch e := ev.(type) {
	case xproto.SelectionRequestEvent:
		h.handleSelectionRequest(e)
	case xproto.MappingNotifyEvent:
		h.handleMapping()
	case xproto.ExposeEvent:
		if win := h.findX(e.Window); win != nil && e.Count == 0 {
			win.full, win.renderPending = true, true
		}
	case xproto.ConfigureNotifyEvent:
		if win := h.findX(e.Window); win != nil {
			win.handleConfigure(e)
		}
	case xproto.MotionNotifyEvent:
		if win := h.findX(e.Event); win != nil {
			h.kit.Motion(win.ctx, int(e.EventX), int(e.EventY), modsOf(e.State))
		}
	case xproto.LeaveNotifyEvent:
		if win := h.findX(e.Event); win != nil {
			win.handleLeave(e)
		}
	case xproto.ButtonPressEvent:
		if win := h.findX(e.Event); win != nil {
			win.handlePress(e)
		}
	case xproto.ButtonReleaseEvent:
		if win := h.findX(e.Event); win != nil {
			win.handleRelease(e)
		}
	case xproto.KeyPressEvent:
		if win := h.findX(e.Event); win != nil {
			win.handleKey(e.Detail, e.State, true)
		}
	case xproto.KeyReleaseEvent:
		if win := h.findX(e.Event); win != nil && !h.events.isAutorepeat(e) {
			win.handleKey(e.Detail, e.State, false)
		}
	case xproto.ClientMessageEvent:
		if win := h.findX(e.Window); win != nil && e.Data.Data32[0] == uint32(h.atoms.delete) {
			win.close()
		}
	}
}

// handleMapping reads the keymap again after a layout change. The C host never does, so
// its windows keep the keymap they started with until XLookupString refreshes it.
func (h *Host) handleMapping() {
	kb, err := newKeyboard(h.c)
	if err != nil {
		fmt.Fprintf(os.Stderr, "bui: cannot read the new keymap: %v\n", err)

		return
	}

	h.keyboard = kb
}

func (win *window) handleConfigure(e xproto.ConfigureNotifyEvent) {
	if int(e.Width) != win.w || int(e.Height) != win.h {
		win.w, win.h = int(e.Width), int(e.Height)
		win.renderPending = true
	}
}

// outside is where a pointer that left the window goes, as the C host has it.
const outside = -100000

func (win *window) handleLeave(e xproto.LeaveNotifyEvent) {
	const held = xproto.KeyButMaskButton1 | xproto.KeyButMaskButton2 | xproto.KeyButMaskButton3

	if e.State&held == 0 {
		win.host.kit.Motion(win.ctx, outside, outside, 0)
	}
}

func modsOf(state uint16) int {
	m := 0

	for _, b := range []struct {
		state uint16
		mod   int
	}{
		{xproto.KeyButMaskShift, engine.ModShift},
		{xproto.KeyButMaskControl, engine.ModCtrl},
		{xproto.KeyButMaskMod1, engine.ModAlt},
		{xproto.KeyButMaskButton1, engine.ModButton1},
	} {
		if state&b.state != 0 {
			m |= b.mod
		}
	}

	return m
}

// The X button numbers past the left, middle and right ones.
const (
	buttonLeft      = 1
	buttonMiddle    = 2
	buttonRight     = 3
	buttonWheelUp   = 4
	buttonWheelDown = 5
	buttonTiltLeft  = 6
	buttonTiltRight = 7
)

func (win *window) handlePress(e xproto.ButtonPressEvent) {
	k, x, y := win.host.kit, int(e.EventX), int(e.EventY)
	mods := modsOf(e.State)
	noButton := mods &^ engine.ModButton1

	switch e.Detail {
	case buttonLeft:
		xproto.SetInputFocus(win.host.c, xproto.InputFocusParent, win.xid, xproto.TimeCurrentTime)
		k.PressMods(win.ctx, x, y, mods|engine.ModButton1)
	case buttonMiddle:
		k.Button(win.ctx, x, y, buttonMiddle, false, mods)
	case buttonRight:
		k.ContextMenu(win.ctx, x, y, mods)
	case buttonWheelUp:
		k.Scroll(win.ctx, x, y, 0, 1, noButton)
	case buttonWheelDown:
		k.Scroll(win.ctx, x, y, 0, -1, noButton)
	case buttonTiltLeft:
		k.Scroll(win.ctx, x, y, -1, 0, noButton)
	case buttonTiltRight:
		k.Scroll(win.ctx, x, y, 1, 0, noButton)
	}
}

func (win *window) handleRelease(e xproto.ButtonReleaseEvent) {
	k, x, y := win.host.kit, int(e.EventX), int(e.EventY)
	mods := modsOf(e.State) &^ engine.ModButton1

	switch e.Detail {
	case buttonLeft:
		k.ReleaseMods(win.ctx, x, y, mods)
	case buttonMiddle, buttonRight:
		k.Button(win.ctx, x, y, int(e.Detail), true, mods)
	}
}

// handleKey passes control characters on as a keysym only, as they are not text.
func (win *window) handleKey(code xproto.Keycode, state uint16, press bool) {
	sym := win.host.keyboard.lookup(byte(code), state)
	name, mods := keysymName(sym), modsOf(state)

	if !press {
		win.host.kit.KeyRelease(win.ctx, name, mods)

		return
	}

	const (
		firstPrintable = 0x20
		del            = 0x7f
	)

	text := keysymText(sym)
	if text != "" && (text[0] < firstPrintable || text[0] == del || mods&engine.ModCtrl != 0) {
		text = ""
	}

	win.host.kit.Key(win.ctx, text, name, mods)
}
