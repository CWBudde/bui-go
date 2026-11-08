// SPDX-License-Identifier: GPL-2.0-or-later

package cocoa

import (
	"unsafe"

	"github.com/CWBudde/bui-go/internal/engine"
	"github.com/ebitengine/purego/objc"
)

// NSEventModifierFlags.
const (
	flagShift   = 1 << 17
	flagControl = 1 << 18
	flagOption  = 1 << 19
	flagCommand = 1 << 20
)

// Virtual key codes the kit gets a keysym for whatever the layout, from HIToolbox's
// Events.h. The first group types no text, and the second is the keypad, which does.
var (
	specialKeysyms = map[uint16]string{
		0x24: "Return",
		0x30: "Tab",
		0x33: "BackSpace",
		0x35: "Escape",
		0x40: "F17",
		0x4C: "KP_Enter",
		0x4F: "F18",
		0x50: "F19",
		0x5A: "F20",
		0x60: "F5",
		0x61: "F6",
		0x62: "F7",
		0x63: "F3",
		0x64: "F8",
		0x65: "F9",
		0x67: "F11",
		0x69: "F13",
		0x6A: "F16",
		0x6B: "F14",
		0x6D: "F10",
		0x6F: "F12",
		0x71: "F15",
		0x72: "Insert", // Help, which is Insert on PC keyboards
		0x73: "Home",
		0x74: "Prior",
		0x75: "Delete",
		0x76: "F4",
		0x77: "End",
		0x78: "F2",
		0x79: "Next",
		0x7A: "F1",
		0x7B: "Left",
		0x7C: "Right",
		0x7D: "Down",
		0x7E: "Up",
	}
	keypadKeysyms = map[uint16]string{
		0x41: "KP_Decimal",
		0x43: "KP_Multiply",
		0x45: "KP_Add",
		0x4B: "KP_Divide",
		0x4E: "KP_Subtract",
		0x51: "KP_Equal",
		0x52: "KP_0",
		0x53: "KP_1",
		0x54: "KP_2",
		0x55: "KP_3",
		0x56: "KP_4",
		0x57: "KP_5",
		0x58: "KP_6",
		0x59: "KP_7",
		0x5B: "KP_8",
		0x5C: "KP_9",
	}
)

const keyCodeTab = 0x30

// modifierKeys are the keys a flagsChanged event reports, as the kit knows them.
// Command is Tk's Meta_L.
var modifierKeys = [...]struct {
	bit    uint64
	keysym string
}{
	{flagShift, "Shift_L"},
	{flagControl, "Control_L"},
	{flagOption, "Alt_L"},
	{flagCommand, "Meta_L"},
}

var (
	selKeyCode        = objc.RegisterName("keyCode")
	selCharacters     = objc.RegisterName("characters")
	selCharsIgnoring  = objc.RegisterName("charactersIgnoringModifiers")
	selLength         = objc.RegisterName("length")
	selCharacterAt    = objc.RegisterName("characterAtIndex:")
	selUTF8String     = objc.RegisterName("UTF8String")
	selModifierFlags  = objc.RegisterName("modifierFlags")
	selPressedButtons = objc.RegisterName("pressedMouseButtons")
)

// isTextChar rules out control characters and AppKit's function-key range.
func isTextChar(c uint16) bool {
	return c >= 0x20 && c != 0x7f && (c < 0xF700 || c > 0xF8FF)
}

// goString copies an NSString, which may be nil.
func goString(s objc.ID) string {
	if s == 0 {
		return ""
	}

	p := objc.Send[*byte](s, selUTF8String)
	if p == nil {
		return ""
	}

	n := 0
	for *(*byte)(unsafe.Add(unsafe.Pointer(p), n)) != 0 {
		n++
	}

	return string(unsafe.Slice(p, n))
}

func firstChar(s objc.ID) (uint16, bool) {
	if s == 0 || objc.Send[uint64](s, selLength) == 0 {
		return 0, false
	}

	return objc.Send[uint16](s, selCharacterAt, uint64(0)), true
}

// keysymOf follows the layout: the character ignoring modifiers except Shift.
func keysymOf(ev objc.ID, code uint16) string {
	if ks, ok := specialKeysyms[code]; ok {
		return ks
	}

	if ks, ok := keypadKeysyms[code]; ok {
		return ks
	}

	ignoring := ev.Send(selCharsIgnoring)

	c, ok := firstChar(ignoring)

	switch {
	case !ok:
		return ""
	case c == ' ':
		return "space"
	case c == '-':
		return "minus"
	case isTextChar(c):
		return goString(ignoring)
	}

	return ""
}

// typedText is empty for dead keys, for keys that are no text and with Command held.
func typedText(ev objc.ID, code uint16, flags uint64) string {
	if _, special := specialKeysyms[code]; special || flags&flagCommand != 0 {
		return ""
	}

	chars := ev.Send(selCharacters)
	if c, ok := firstChar(chars); ok && isTextChar(c) {
		return goString(chars)
	}

	return ""
}

func (win *window) handleKey(ev objc.ID, press bool) {
	h := win.host
	h.ignoreMomentum = true // a key press ends momentum scrolling

	pool := poolPush()
	defer poolPop(pool)

	flags := objc.Send[uint64](ev, selModifierFlags)
	code := objc.Send[uint16](ev, selKeyCode)
	mods := h.modsOf(flags)
	keysym := keysymOf(ev, code)

	if !press {
		win.kit().KeyRelease(win.ctx, keysym, mods)

		return
	}

	win.kit().Key(win.ctx, typedText(ev, code, flags), keysym, mods)
}

// handleFlags sends modifier presses and releases as keys.
func (win *window) handleFlags(flags uint64) {
	h := win.host
	old := win.flags
	win.flags = flags
	h.ignoreMomentum = true

	mods := h.modsOf(flags)

	for _, key := range modifierKeys {
		if (flags^old)&key.bit == 0 || win.nswin == 0 {
			continue
		}

		if flags&key.bit != 0 {
			win.kit().Key(win.ctx, "", key.keysym, mods)
		} else {
			win.kit().KeyRelease(win.ctx, key.keysym, mods)
		}
	}
}

// modsOf maps Command to Ctrl, as Blender's macOS keymaps use it for Ctrl's shortcuts.
func (h *Host) modsOf(flags uint64) int {
	mods := 0

	if flags&flagShift != 0 {
		mods |= engine.ModShift
	}

	if flags&(flagControl|flagCommand) != 0 {
		mods |= engine.ModCtrl
	}

	if flags&flagOption != 0 {
		mods |= engine.ModAlt
	}

	if h.buttons()&1 != 0 {
		mods |= engine.ModButton1
	}

	return mods
}
