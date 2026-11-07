// SPDX-License-Identifier: GPL-2.0-or-later

package x11

import (
	"encoding/binary"
	"errors"
	"fmt"

	"github.com/jezek/xgb"
	"github.com/jezek/xgb/xproto"
)

// xgb has no XKB package, so this writes out the two requests the host needs, from the
// XKB protocol specification: UseExtension and GetMap.
const (
	xkbMinorUseExtension = 0
	xkbMinorSelectEvents = 1
	xkbMinorGetMap       = 8
	xkbUseCoreKeyboard   = 0x0100
	xkbMapKeyTypes       = 1 << 0
	xkbMapKeySyms        = 1 << 1

	// What follows the fixed part of GetMap's reply, a key type's and map entry's wire
	// sizes, and where the reply's fields are.
	xkbReplyHeader   = 40
	xkbTypeSize      = 8
	xkbEntrySize     = 8
	xkbPreserveSize  = 4
	xkbSymMapSize    = 8
	xkbKeysymSize    = 4
	xkbReplyMinCode  = 10
	xkbReplyNTypes   = 15
	xkbReplyNKeySyms = 20
	xkbMaxGroups     = 4
)

// How a group past a key's own is brought into range, from a key's group info.
const (
	xkbGroupCountMask   = 0x0f
	xkbOutOfRangeMask   = 0xc0
	xkbOutOfRangeClamp  = 0x40
	xkbOutOfRangeTarget = 0x80
	xkbRedirectShift    = 4
	xkbRedirectMask     = 3
)

var errXKBReply = errors.New("malformed XKB reply")

// xkbEvent is any event of the XKB extension. The host selects only those that mean a new
// keymap, so it needs nothing from one.
type xkbEvent struct{ raw []byte }

// Bytes implements xgb.Event.
func (e xkbEvent) Bytes() []byte { return e.raw }

// String implements xgb.Event.
func (e xkbEvent) String() string { return fmt.Sprintf("xkbEvent%v", e.raw) }

// init registers xkbEvent for every extension event number, as XKB's depends on the
// server. xgb's own extensions write this global map when they connect, which races with
// the reading goroutine of any connection already open. Here it happens before there is
// one, and XKB is the only extension whose events the host selects.
func init() {
	const firstExtensionEvent, lastEvent = 64, 127

	for n := firstExtensionEvent; n <= lastEvent; n++ {
		xgb.NewEventFuncs[n] = func(raw []byte) xgb.Event { return xkbEvent{raw: raw} }
	}
}

// enableXKB tells the server this client understands XKB's event state, as libX11 does,
// and returns the extension's opcode, 0 without XKB. Without this a second layout could
// not work: the server reports group 2 to other clients as Mod5, the same bit as AltGr.
// With it, events carry the group in bits 13 and 14.
//
// It also ends core MappingNotify events for this client, so it selects XKB's own in their
// place.
func enableXKB(c *xgb.Conn) byte {
	const (
		name          = "XKEYBOARD"
		requestLength = 2 // in 4-byte units
		versionMajor  = 1
		statusOffset  = 1
	)

	ext, err := xproto.QueryExtension(c, uint16(len(name)), name).Reply()
	if err != nil || !ext.Present {
		return 0
	}

	buf := make([]byte, 4*requestLength)
	buf[0] = ext.MajorOpcode
	buf[1] = xkbMinorUseExtension
	xgb.Put16(buf[2:], requestLength)
	xgb.Put16(buf[4:], versionMajor)

	cookie := c.NewCookie(true, true)
	c.NewRequest(buf, cookie)

	reply, err := cookie.Reply()
	if err != nil || len(reply) <= statusOffset || reply[statusOffset] == 0 {
		return 0
	}

	selectKeymapEvents(c, ext.MajorOpcode)

	return ext.MajorOpcode
}

// selectKeymapEvents asks for XkbNewKeyboardNotify, in all its detail, and XkbMapNotify,
// whose detail has fields of its own, for every part of the map. That is what makes the
// request need no lists after it.
func selectKeymapEvents(c *xgb.Conn, opcode byte) {
	const (
		requestLength  = 4 // in 4-byte units
		newKeyboard    = 1 << 0
		mapNotify      = 1 << 1
		everyMapChange = 0xffff
	)

	buf := make([]byte, 4*requestLength)
	buf[0] = opcode
	buf[1] = xkbMinorSelectEvents
	xgb.Put16(buf[2:], requestLength)
	xgb.Put16(buf[4:], xkbUseCoreKeyboard)
	xgb.Put16(buf[6:], newKeyboard|mapNotify) // affectWhich
	xgb.Put16(buf[10:], newKeyboard)          // selectAll
	xgb.Put16(buf[12:], everyMapChange)       // affectMap
	xgb.Put16(buf[14:], everyMapChange)       // map

	c.NewRequest(buf, c.NewCookie(false, false))
}

// xkbEntry maps a modifier combination to a level of a key type.
type xkbEntry struct {
	active   bool
	mods     uint16
	level    int
	preserve uint16
}

// xkbType is a key type: which modifiers it looks at and what they select.
type xkbType struct {
	mask    uint16
	entries []xkbEntry
}

// xkbKey is one key's keysyms, width to a group, and each group's type.
type xkbKey struct {
	types     [xkbMaxGroups]int
	groupInfo byte
	width     int
	syms      []uint32
}

// xkbMap is XKB's own keymap, which tells what the core keymap cannot: how many levels
// each group of each key has, and so which column is which.
type xkbMap struct {
	types   []xkbType
	minCode int
	keys    []xkbKey
}

// fetchXKBMap asks the server for the map.
func fetchXKBMap(c *xgb.Conn, opcode byte) (*xkbMap, error) {
	const requestLength = 7 // in 4-byte units

	buf := make([]byte, 4*requestLength)
	buf[0] = opcode
	buf[1] = xkbMinorGetMap
	xgb.Put16(buf[2:], requestLength)
	xgb.Put16(buf[4:], xkbUseCoreKeyboard)
	xgb.Put16(buf[6:], xkbMapKeyTypes|xkbMapKeySyms) // full: all of these, no ranges to fill in

	cookie := c.NewCookie(true, true)
	c.NewRequest(buf, cookie)

	reply, err := cookie.Reply()
	if err != nil {
		return nil, fmt.Errorf("XKB GetMap: %w", err)
	}

	return parseXKBMap(reply)
}

// parseXKBMap reads GetMap's reply. It trusts nothing about the lengths, as a slice
// bound failing here would take the whole program down over a keymap.
func parseXKBMap(reply []byte) (*xkbMap, error) {
	if len(reply) < xkbReplyHeader {
		return nil, errXKBReply
	}

	m := &xkbMap{minCode: int(reply[xkbReplyMinCode])}
	r := xkbReader{data: reply[xkbReplyHeader:]}

	m.types = make([]xkbType, reply[xkbReplyNTypes])
	for i := range m.types {
		m.types[i] = r.keyType()
	}

	m.keys = make([]xkbKey, reply[xkbReplyNKeySyms])
	for i := range m.keys {
		m.keys[i] = r.key()
	}

	if r.failed {
		return nil, errXKBReply
	}

	return m, nil
}

// xkbReader walks the reply, remembering whether it ever ran short.
type xkbReader struct {
	data   []byte
	failed bool
}

func (r *xkbReader) take(n int) []byte {
	if n > len(r.data) {
		r.failed = true
		r.data = nil

		return make([]byte, n)
	}

	b := r.data[:n]
	r.data = r.data[n:]

	return b
}

func (r *xkbReader) keyType() xkbType {
	b := r.take(xkbTypeSize)
	t := xkbType{mask: uint16(b[0]), entries: make([]xkbEntry, b[5])}
	hasPreserve := b[6] != 0

	for i := range t.entries {
		e := r.take(xkbEntrySize)
		t.entries[i] = xkbEntry{active: e[0] != 0, mods: uint16(e[1]), level: int(e[2])}
	}

	if hasPreserve {
		for i := range t.entries {
			t.entries[i].preserve = uint16(r.take(xkbPreserveSize)[0])
		}
	}

	return t
}

func (r *xkbReader) key() xkbKey {
	b := r.take(xkbSymMapSize)
	k := xkbKey{groupInfo: b[4], width: int(b[5])}

	for i := range k.types {
		k.types[i] = int(b[i])
	}

	k.syms = make([]uint32, binary.LittleEndian.Uint16(b[6:]))
	for i := range k.syms {
		k.syms[i] = binary.LittleEndian.Uint32(r.take(xkbKeysymSize))
	}

	return k
}

// groupOf brings a group into a key's range, as XkbTranslateKeyCode does. ok is false for
// a key with no groups.
func (k *xkbKey) groupOf(group int) (int, bool) {
	count := int(k.groupInfo & xkbGroupCountMask)
	if count == 0 {
		return 0, false
	}

	if group < count {
		return group, true
	}

	switch k.groupInfo & xkbOutOfRangeMask {
	case xkbOutOfRangeClamp:
		return count - 1, true
	case xkbOutOfRangeTarget:
		target := int(k.groupInfo>>xkbRedirectShift) & xkbRedirectMask
		if target >= count {
			return 0, true
		}

		return target, true
	}

	return group % count, true
}

// lookup translates a keycode and an event's state, as XkbTranslateKeyCode followed by
// XLookupString's two rules: Ctrl on a non-Latin keysym looks for a Latin one in the
// key's other groups, so that Ctrl+C works on a Cyrillic layout, and a Lock the key's type
// did not use uppercases the result.
func (m *xkbMap) lookup(code byte, state uint16) uint32 {
	const (
		groupShift = 13
		groupMask  = 3
		lastLatin  = 0x7f
	)

	i := int(code) - m.minCode
	if i < 0 || i >= len(m.keys) {
		return 0
	}

	key := &m.keys[i]

	group, ok := key.groupOf(int(state>>groupShift) & groupMask)
	if !ok {
		return 0
	}

	sym, consumed := m.symOf(key, group, state)

	if state&modControl != 0 && sym > lastLatin {
		sym = m.latinOf(key, state, sym)
	}

	if state&modLock != 0 && consumed&modLock == 0 {
		_, upper := convertCase(sym)

		return upper
	}

	return sym
}

// latinOf returns the first Latin keysym in the key's groups, or sym if it has none.
func (m *xkbMap) latinOf(key *xkbKey, state uint16, sym uint32) uint32 {
	const lastLatin = 0x7f

	for g := range int(key.groupInfo & xkbGroupCountMask) {
		if latin, _ := m.symOf(key, g, state); latin != 0 && latin <= lastLatin {
			return latin
		}
	}

	return sym
}

// symOf returns a group's keysym for the state, and the modifiers its type used up.
func (m *xkbMap) symOf(key *xkbKey, group int, state uint16) (sym uint32, consumed uint16) {
	col := group * key.width

	if t := key.types[group]; t < len(m.types) {
		typ := &m.types[t]
		consumed = typ.mask

		for j := range typ.entries {
			e := &typ.entries[j]
			if e.active && state&typ.mask == e.mods {
				col += e.level
				consumed &^= e.preserve

				break
			}
		}
	}

	if col >= len(key.syms) {
		return 0, consumed
	}

	return key.syms[col], consumed
}
