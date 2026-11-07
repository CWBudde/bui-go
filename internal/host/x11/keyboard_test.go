// SPDX-License-Identifier: GPL-2.0-or-later

package x11

import (
	"encoding/binary"
	"slices"
	"testing"
)

// testCoreKeyboard is a keymap for a server without XKB, whose rules are the protocol's.
func testCoreKeyboard() *keyboard {
	const columns = 4

	rows := [][columns]uint32{
		{'q', 'Q', 0, 0},                     // 10
		{'1', '!', 0, 0},                     // 11
		{0xff95, 0xffb7, 0, 0},               // 12: KP_Home, KP_7
		{0xe4, 0xc4, 0, 0},                   // 13: adiaeresis
		{0xff08, 0xff08, 0, 0},               // 14: BackSpace
		{0xfe51, 0, 0, 0},                    // 15: dead_acute
		{'a', 0, 0, 0},                       // 16: a lone letter
		{0x6c0, 0x6e0, 0, 0},                 // 17: Cyrillic yu, whose keysyms are not Latin-1
		{0, 0, 0, 0},                         // 18: nothing
		{unicodeKeysym | 0x20ac, 0, 0, 0},    // 19: a Unicode keysym
		{unicodeKeysym | 0x1f600, 0, 0, 0},   // 20: past the BMP
		{'x', 'X', unicodeKeysym | 0x3b1, 0}, // 21: a second group
		{0xff0d, 0, 0, 0},                    // 22: Return
	}

	k := &keyboard{minCode: 10, perCode: columns, numLock: 1 << 4, modeSwitch: 1 << 6, capsLock: true}

	for _, r := range rows {
		k.syms = append(k.syms, r[:]...)
	}

	return k
}

func TestCoreLookup(t *testing.T) {
	const (
		num   = uint16(1 << 4)
		group = uint16(1 << 6)
	)

	k := testCoreKeyboard()

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
		{"digit ignores caps", 11, modLock, '1', "1", "1"},
		{"digit with shift", 11, modShift, '!', "exclam", "!"},
		{"keypad without num lock", 12, 0, 0xff95, "KP_Home", ""},
		{"keypad with num lock", 12, num, 0xffb7, "KP_7", "7"},
		{"keypad, num lock and shift", 12, num | modShift, 0xff95, "KP_Home", ""},
		{"umlaut with caps", 13, modLock, 0xc4, "Adiaeresis", "Ä"},
		{"control code", 14, 0, 0xff08, "BackSpace", "\b"},
		{"dead key types itself", 15, 0, 0xfe51, "dead_acute", "´"},
		{"lone letter gets its other case", 16, modShift, 'A', "A", "A"},
		{"non-Latin case", 17, modLock, 0x6e0, "Cyrillic_YU", "Ю"},
		{"no keysym", 18, 0, 0, "", ""},
		{"unknown code", 200, 0, 0, "", ""},
		{"unicode keysym", 19, 0, unicodeKeysym | 0x20ac, "U20AC", "€"},
		{"unicode past the BMP", 20, 0, unicodeKeysym | 0x1f600, "U01F600", "😀"},
		{"second group", 21, group, unicodeKeysym | 0x3b1, "U03B1", "α"},
		{"second group falls back", 10, group, 'q', "q", "q"},
		{"return", 22, 0, 0xff0d, "Return", "\r"},
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

// xkbReply builds a GetMap reply. Two types: ONE_LEVEL, and one where Shift selects level
// 2 and Mod5 level 3, with a preserved Lock for coverage. Keys start at code 10.
func xkbReply(t *testing.T) []byte {
	t.Helper()

	const mod5 = 1 << 7

	body := []byte{
		// type 0: no modifiers, no entries
		0, 0, 0, 0, 1, 0, 0, 0,
		// type 1: Shift and Mod5, three entries, with preserve
		modShift8 | mod5, 0, 0, 0, 3, 3, 1, 0,
		1, modShift8, 1, 0, 0, 0, 0, 0,
		1, mod5, 2, 0, 0, 0, 0, 0,
		0, modShift8 | mod5, 3, 0, 0, 0, 0, 0, // inactive
		0, 0, 0, 0, mod5, 0, 0, 0, 0, 0, 0, 0,
	}

	key := func(types [4]byte, info, width byte, syms ...uint32) {
		body = append(body, types[0], types[1], types[2], types[3], info, width,
			byte(len(syms)), byte(len(syms)>>8))

		for _, s := range syms {
			body = binary.LittleEndian.AppendUint32(body, s)
		}
	}

	key([4]byte{1, 1}, 2, 3, 'a', 'A', '@', 'b', 'B', '#') // 10: two groups of three levels
	key([4]byte{0}, 1, 1, 0xff0d)                          // 11: one group, one level
	key([4]byte{0}, 0, 0)                                  // 12: no groups
	key([4]byte{1, 0}, 0x42, 3, 'c', 'C', '$', 'd', 0, 0)  // 13: clamps a group past its two
	key([4]byte{0, 0}, 0x92, 1, 'e', 'f')                  // 14: redirects to group 2 (index 1)
	key([4]byte{99}, 1, 1, 'g')                            // 15: a type that does not exist
	key([4]byte{0}, 1, 1, 'h')                             // 16: one level, so Lock is left over
	key([4]byte{0, 0}, 2, 1, 0x6c1, 'i')                   // 17: Cyrillic, then Latin
	key([4]byte{0}, 1, 1, 0x6c2)                           // 18: no Latin keysym at all

	var header [xkbReplyHeader]byte

	header[xkbReplyMinCode] = 10
	header[xkbReplyNTypes] = 2
	header[xkbReplyNKeySyms] = 9

	return slices.Concat(header[:], body)
}

const modShift8 = byte(modShift)

func TestXKBLookup(t *testing.T) {
	const (
		group2 = uint16(1 << 13)
		group3 = uint16(2 << 13)
		mod5   = uint16(1 << 7)
	)

	m, err := parseXKBMap(xkbReply(t))
	if err != nil {
		t.Fatal(err)
	}

	for _, tc := range []struct {
		name  string
		code  byte
		state uint16
		want  uint32
	}{
		{"first level", 10, 0, 'a'},
		{"shift", 10, modShift, 'A'},
		{"mod5 is a level, not a group", 10, mod5, '@'},
		{"second group", 10, group2, 'b'},
		{"second group, shift", 10, group2 | modShift, 'B'},
		{"second group, mod5", 10, group2 | mod5, '#'},
		{"an inactive entry stays level 1", 10, modShift | mod5, 'a'},
		{"other modifiers do not count", 10, 1 << 3, 'a'},
		{"a group past the key's wraps", 11, group2, 0xff0d},
		{"no groups", 12, 0, 0},
		{"clamped into range", 13, group3, 'd'},
		{"redirected", 14, group3, 'f'},
		{"an unknown type stays at level 1", 15, 0, 'g'},
		{"lock the type did not use uppercases", 16, modLock, 'H'},
		{"below the first key", 9, 0, 0},
		{"past the last key", 19, 0, 0},
		{"non-Latin without ctrl", 17, 0, 0x6c1},
		{"ctrl finds the Latin group", 17, modControl, 'i'},
		{"ctrl with no Latin group", 18, modControl, 0x6c2},
	} {
		if got := m.lookup(tc.code, tc.state); got != tc.want {
			t.Errorf("%s: lookup = %#x, want %#x", tc.name, got, tc.want)
		}
	}
}

// A short reply is an error, at any length, and never a panic.
func TestXKBReplyTruncated(t *testing.T) {
	reply := xkbReply(t)

	for n := range reply {
		if _, err := parseXKBMap(reply[:n]); err == nil {
			t.Errorf("a reply cut to %d of %d bytes parsed", n, len(reply))
		}
	}
}

func TestKeysymTableIsSorted(t *testing.T) {
	for i := 1; i < len(keysymInfo); i++ {
		if keysymInfo[i-1].sym >= keysymInfo[i].sym {
			t.Fatalf("%#x before %#x", keysymInfo[i-1].sym, keysymInfo[i].sym)
		}
	}
}
