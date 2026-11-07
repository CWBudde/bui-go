// SPDX-License-Identifier: GPL-2.0-or-later

package x11

import "testing"

// A German key row by hand, in the columns an XKB server's core keymap uses, so this
// runs with no server. keyboard_xlib_test.go compares whole keymaps against libX11.
func testKeyboard() *keyboard {
	const (
		xkKP7, xkKPHome = 0xffb7, 0xff95
		xkAt            = '@'
		xkBackSpace     = 0xff08
		xkBreak         = 0xff6b
		xkSysReq        = 0xff15
		xkDeadAcute     = 0xfe51
		xkAdiaeresis    = 0xe4
		xkADiaeresis    = 0xc4
		columns         = 6
	)

	rows := [][columns]uint32{
		{'q', 'Q', 'q', 'Q', xkAt, 0},              // 10: AltGr+q is @
		{'1', '!', '1', '!', 0, 0},                 // 11
		{xkKPHome, xkKP7, 0, 0, 0, 0},              // 12
		{xkAdiaeresis, xkADiaeresis, 0, 0, 0, 0},   // 13
		{xkBackSpace, xkBackSpace, 0, 0, 0, 0},     // 14
		{xkPause, xkBreak, 0, 0, 0, 0},             // 15
		{xkPrint, xkSysReq, 0, 0, 0, 0},            // 16
		{xkDeadAcute, 0, 0, 0, 0, 0},               // 17
		{'a', 0, 0, 0, 0, 0},                       // 18: a lone letter
		{0x6c0, 0x6e0, 0, 0, 0, 0},                 // 19: Cyrillic yu, whose keysyms are not Latin-1
		{0, 0, 0, 0, 0, 0},                         // 20: nothing
		{unicodeKeysym | 0x20ac, 0, 0, 0, 0, 0},    // 21: a Unicode keysym
		{unicodeKeysym | 0x1f600, 0, 0, 0, 0, 0},   // 22: past the BMP
		{'x', 'X', unicodeKeysym | 0x3b1, 0, 0, 0}, // 23: a second group
		{0xff0d, 0, 0, 0, 0, 0},                    // 24: Return
	}

	k := &keyboard{
		minCode: 10, perCode: columns, numLock: 1 << 4, modeSwitch: 1 << 6, level3: 1 << 7, capsLock: true,
	}

	for _, r := range rows {
		k.syms = append(k.syms, r[:]...)
	}

	return k
}

func TestLookup(t *testing.T) {
	const (
		num   = uint16(1 << 4)
		group = uint16(1 << 6)
		lvl3  = uint16(1 << 7)
	)

	k := testKeyboard()

	for _, tc := range []struct {
		name  string
		code  byte
		state uint16
		sym   uint32
		key   string
		text  string
	}{
		{"plain letter", 10, 0, 'q', "q", "q"},
		{"shift", 10, modShift, 'Q', "Q", "Q"},
		{"caps", 10, modLock, 'Q', "Q", "Q"},
		{"shift and caps cancel", 10, modShift | modLock, 'q', "q", "q"},
		{"altgr", 10, lvl3, '@', "at", "@"},
		{"digit ignores caps", 11, modLock, '1', "1", "1"},
		{"digit with shift", 11, modShift, '!', "exclam", "!"},
		{"keypad without num lock", 12, 0, 0xff95, "KP_Home", ""},
		{"keypad with num lock", 12, num, 0xffb7, "KP_7", "7"},
		{"keypad, num lock and shift", 12, num | modShift, 0xff95, "KP_Home", ""},
		{"umlaut with caps", 13, modLock, 0xc4, "Adiaeresis", "Ä"},
		{"control code", 14, 0, 0xff08, "BackSpace", "\b"},
		{"pause", 15, modShift, xkPause, "Pause", ""},
		{"break needs ctrl", 15, modControl, 0xff6b, "Break", ""},
		{"print", 16, modShift, xkPrint, "Print", ""},
		{"sys req needs alt", 16, modAlt, 0xff15, "Sys_Req", ""},
		{"dead key types nothing", 17, 0, 0xfe51, "dead_acute", ""},
		{"lone letter gets its other case", 18, modShift, 'A', "A", "A"},
		{"non-Latin case", 19, modLock, 0x6e0, "Cyrillic_YU", "Ю"},
		{"no keysym", 20, 0, 0, "", ""},
		{"unknown code", 200, 0, 0, "", ""},
		{"unicode keysym", 21, 0, unicodeKeysym | 0x20ac, "U20AC", "€"},
		{"unicode past the BMP", 22, 0, unicodeKeysym | 0x1f600, "U01F600", "😀"},
		{"second group", 23, group, unicodeKeysym | 0x3b1, "U03B1", "α"},
		{"second group falls back", 10, group, 'q', "q", "q"},
		{"return", 24, 0, 0xff0d, "Return", "\r"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			sym := k.lookup(tc.code, tc.state)
			if sym != tc.sym {
				t.Fatalf("lookup = %#x, want %#x", sym, tc.sym)
			}

			if got := keysymName(sym); got != tc.key {
				t.Errorf("name = %q, want %q", got, tc.key)
			}

			if got := keysymText(sym); got != tc.text {
				t.Errorf("text = %q, want %q", got, tc.text)
			}
		})
	}
}

func TestKeysymTableIsSorted(t *testing.T) {
	for i := 1; i < len(keysymInfo); i++ {
		if keysymInfo[i-1].sym >= keysymInfo[i].sym {
			t.Fatalf("%#x before %#x", keysymInfo[i-1].sym, keysymInfo[i].sym)
		}
	}
}
