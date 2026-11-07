// SPDX-License-Identifier: GPL-2.0-or-later

package x11

import (
	"fmt"
	"slices"
	"unicode"
	"unicode/utf8"

	"github.com/jezek/xgb"
	"github.com/jezek/xgb/xproto"
)

// Keysyms the translation has to recognize.
const (
	xkModeSwitch = 0xff7e
	xkNumLock    = 0xff7f
	xkCapsLock   = 0xffe5
	xkKPSpace    = 0xff80
	xkKPEqual    = 0xffbd
	xkDelete     = 0xffff
	// unicodeKeysym marks a keysym that holds its character directly.
	unicodeKeysym = 0x01000000
)

// Modifier state bits of an X event.
const (
	modLock    = uint16(xproto.ModMaskLock)
	modShift   = uint16(xproto.ModMaskShift)
	modControl = uint16(xproto.ModMaskControl)
)

// keyboard is the server's keymap. xkb is XKB's own map, which is all libX11 uses. Only
// a server without XKB, which is rare, falls back on the core request and its protocol
// rules, in the other fields.
type keyboard struct {
	xkb *xkbMap
	// opcode is XKB's, 0 without it. A new keymap has to be fetched the same way.
	opcode byte

	minCode byte
	perCode int
	syms    []uint32

	numLock, modeSwitch uint16
	capsLock            bool
}

// newKeyboard asks the server for its keymap.
func newKeyboard(c *xgb.Conn, opcode byte) (*keyboard, error) {
	setup := xproto.Setup(c)
	count := byte(setup.MaxKeycode - setup.MinKeycode + 1)

	mapping, err := xproto.GetKeyboardMapping(c, setup.MinKeycode, count).Reply()
	if err != nil {
		return nil, fmt.Errorf("GetKeyboardMapping: %w", err)
	}

	mods, err := xproto.GetModifierMapping(c).Reply()
	if err != nil {
		return nil, fmt.Errorf("GetModifierMapping: %w", err)
	}

	k := &keyboard{
		opcode:  opcode,
		minCode: byte(setup.MinKeycode),
		perCode: int(mapping.KeysymsPerKeycode),
		syms:    make([]uint32, len(mapping.Keysyms)),
	}

	for i, s := range mapping.Keysyms {
		k.syms[i] = uint32(s)
	}

	k.readModifiers(mods)

	if opcode != 0 {
		k.xkb, err = fetchXKBMap(c, opcode)
		if err != nil {
			return nil, err
		}
	}

	return k, nil
}

// readModifiers finds what each modifier means, from the keysyms bound to it. Eight
// modifiers with KeycodesPerModifier keys each, in the order of their state bits.
func (k *keyboard) readModifiers(mods *xproto.GetModifierMappingReply) {
	per := int(mods.KeycodesPerModifier)

	for mod := range 8 {
		for _, code := range mods.Keycodes[mod*per : (mod+1)*per] {
			for _, sym := range k.row(byte(code)) {
				switch sym {
				case xkNumLock:
					k.numLock |= 1 << mod
				case xkModeSwitch:
					k.modeSwitch |= 1 << mod
				case xkCapsLock:
					if 1<<mod == modLock {
						k.capsLock = true
					}
				}
			}
		}
	}
}

// row returns a keycode's keysyms.
func (k *keyboard) row(code byte) []uint32 {
	i := (int(code) - int(k.minCode)) * k.perCode
	if i < 0 || i+k.perCode > len(k.syms) {
		return nil
	}

	return k.syms[i : i+k.perCode]
}

// lookup translates a keycode and an event's modifier state to a keysym, 0 for none.
func (k *keyboard) lookup(code byte, state uint16) uint32 {
	if k.xkb != nil {
		return k.xkb.lookup(code, state)
	}

	row := k.row(code)
	if row == nil {
		return 0
	}

	first, second := k.coreLevels(row, state)

	return k.coreLevel(first, second, state)
}

// coreLevels picks the pair of the group, as the core protocol has it: Mode_switch
// selects the second, and a key with nothing there uses the first.
func (k *keyboard) coreLevels(row []uint32, state uint16) (first, second uint32) {
	if state&k.modeSwitch != 0 {
		if a, s := column(row, 2), column(row, 3); a != 0 || s != 0 {
			return normalize(a, s)
		}
	}

	return normalize(column(row, 0), column(row, 1))
}

func column(row []uint32, i int) uint32 {
	if i < len(row) {
		return row[i]
	}

	return 0
}

// normalize applies the protocol's rule for a pair with a missing keysym: a lone
// letter has its other case as the second, and anything else repeats.
func normalize(a, s uint32) (first, second uint32) {
	if s != 0 {
		return a, s
	}

	lower, upper := convertCase(a)
	if lower != upper {
		return lower, upper
	}

	return a, a
}

// coreLevel chooses between a pair by the core protocol's rules: Num Lock makes a keypad
// key's second keysym the default, and Caps Lock uppercases what Shift selects.
func (k *keyboard) coreLevel(first, second uint32, state uint16) uint32 {
	shift := state&modShift != 0

	if isKeypad(second) && state&k.numLock != 0 {
		return choose(shift, first, second)
	}

	sym := choose(shift, second, first)

	if state&modLock != 0 && k.capsLock {
		_, upper := convertCase(sym)

		return upper
	}

	return sym
}

func choose(v bool, yes, no uint32) uint32 {
	if v {
		return yes
	}

	return no
}

// isKeypad is XK_KP_Space up to XK_KP_Equal, as IsKeypadKey.
func isKeypad(sym uint32) bool { return sym >= xkKPSpace && sym <= xkKPEqual }

// convertCase returns a keysym's lower and upper case, as XConvertCase. Equal results
// mean it has no case.
func convertCase(sym uint32) (lower, upper uint32) {
	r := keysymRune(sym)
	if r == 0 {
		return sym, sym
	}

	// A keysym in the Unicode range keeps its form, as XConvertCase does.
	if sym&0xff000000 == unicodeKeysym {
		l, u := uint32(unicode.ToLower(r)), uint32(unicode.ToUpper(r)) //nolint:gosec // valid runes

		return unicodeKeysym | l, unicodeKeysym | u
	}

	return runeKeysym(unicode.ToLower(r)), runeKeysym(unicode.ToUpper(r))
}

// keysymIndex finds a keysym in the sorted table.
func keysymIndex(sym uint32) (int, bool) {
	return slices.BinarySearchFunc(keysymInfo[:], sym, func(e keysymEntry, s uint32) int {
		switch {
		case e.sym < s:
			return -1
		case e.sym > s:
			return 1
		}

		return 0
	})
}

// keysymName returns a keysym's name, as XKeysymToString, or "" for an unknown one.
func keysymName(sym uint32) string {
	if i, ok := keysymIndex(sym); ok {
		return keysymInfo[i].name
	}

	if sym&0xff000000 == unicodeKeysym {
		if v := sym & 0xffffff; v > 0xffff {
			return fmt.Sprintf("U%06X", v)
		}

		return fmt.Sprintf("U%04X", sym&0xffffff)
	}

	return ""
}

// keysymRune returns the character a keysym stands for, 0 for none.
func keysymRune(sym uint32) rune {
	if i, ok := keysymIndex(sym); ok && keysymInfo[i].r != 0 {
		return keysymInfo[i].r
	}

	if sym&0xff000000 == unicodeKeysym {
		return rune(sym & 0xffffff)
	}

	return 0
}

// runeKeysym is keysymRune's inverse for the case conversion. Latin-1 keysyms are the
// characters themselves, and everything else has a table entry or the Unicode range.
func runeKeysym(r rune) uint32 {
	if r >= 0x20 && r <= 0xff {
		return uint32(r)
	}

	if s, ok := keysymOfRune[r]; ok {
		return s
	}

	return unicodeKeysym | uint32(r) //nolint:gosec // a rune is not negative here
}

// keysymOfRune is the table reversed. Where several keysyms type one character the
// lowest wins, as the older sets come first.
var keysymOfRune = func() map[rune]uint32 {
	m := make(map[rune]uint32, len(keysymInfo))

	for _, e := range keysymInfo {
		if _, taken := m[e.r]; !taken && e.r != 0 {
			m[e.r] = e.sym
		}
	}

	return m
}()

// keysymText returns what a keysym types, as XLookupString would in a UTF-8 locale:
// the function keys with a control code give it, and so do the keypad's characters.
func keysymText(sym uint32) string {
	switch {
	case sym >= 0xff08 && sym <= 0xff0b, sym == 0xff0d, sym == 0xff1b, sym == xkDelete,
		sym == 0xff89, sym == 0xff8d, sym == xkKPSpace, sym >= 0xffaa && sym <= 0xffb9, sym == xkKPEqual:
		if sym == xkKPSpace {
			return " "
		}

		return string(rune(sym & 0x7f))
	}

	r := keysymRune(sym)
	if r == 0 {
		r = deadKeyRune[sym]
	}

	if r == 0 || !utf8.ValidRune(r) {
		return ""
	}

	return string(r)
}

// deadKeyRune is what a dead key types by itself, as in libX11's table. There is no
// Compose here, so without this a ^ key on a German keyboard would type nothing.
var deadKeyRune = map[uint32]rune{
	0xfe50: '`',      // dead_grave
	0xfe51: '\u00b4', // dead_acute
	0xfe52: '^',      // dead_circumflex
	0xfe53: '~',      // dead_tilde
	0xfe54: '\u00af', // dead_macron
	0xfe55: '\u02d8', // dead_breve
	0xfe56: '\u02d9', // dead_abovedot
	0xfe57: '\u00a8', // dead_diaeresis
	0xfe58: '\u02da', // dead_abovering
	0xfe59: '\u02dd', // dead_doubleacute
	0xfe5a: '\u02c7', // dead_caron
	0xfe5b: '\u00b8', // dead_cedilla
	0xfe5c: '\u02db', // dead_ogonek
	0xfe5d: '\u037a', // dead_iota
	0xfe5e: '\u309b', // dead_voiced_sound
	0xfe5f: '\u309c', // dead_semivoiced_sound
}
