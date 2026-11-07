// SPDX-License-Identifier: GPL-2.0-or-later

package x11

import (
	"bytes"

	"github.com/jezek/xgb"
	"github.com/jezek/xgb/xproto"
)

// ClipboardGet implements engine.WindowHost: asks the owner and waits for its answer,
// as GHOST_SystemX11::getClipboard does. Other events wait in the queue meanwhile.
func (win *window) ClipboardGet() (string, bool) {
	h := win.host
	if win.xid == 0 {
		return "", false
	}

	owner, err := xproto.GetSelectionOwner(h.c, h.atoms.clipboard).Reply()
	if err != nil {
		return "", false
	}

	// Asking ourselves would time out, as nothing answers while this waits.
	if h.findX(owner.Owner) != nil && h.hasClip {
		return h.clip, true
	}

	xproto.ConvertSelection(h.c, win.xid, h.atoms.clipboard, h.atoms.utf8, h.atoms.selection, xproto.TimeCurrentTime)

	ev := h.events.waitFor(clipboardTimeout, func(ev xgb.Event) bool {
		sn, ok := ev.(xproto.SelectionNotifyEvent)

		return ok && sn.Requestor == win.xid
	})

	sn, ok := ev.(xproto.SelectionNotifyEvent)
	if !ok || sn.Property == xproto.AtomNone {
		return "", false
	}

	return win.readSelection()
}

// readSelection takes the text the clipboard's owner stored on this window.
func (win *window) readSelection() (string, bool) {
	const maxClipboardLongs = 1 << 24

	prop, err := xproto.GetProperty(win.host.c, true, win.xid, win.host.atoms.selection, xproto.GetPropertyTypeAny,
		0, maxClipboardLongs).Reply()
	if err != nil || prop == nil {
		return "", false
	}

	// C strings end at a NUL, wherever the text came from.
	value := prop.Value
	if end := bytes.IndexByte(value, 0); end >= 0 {
		value = value[:end]
	}

	return string(value), true
}

// ClipboardSet implements engine.WindowHost.
func (win *window) ClipboardSet(text string) {
	h := win.host
	h.clip, h.hasClip = text, true

	if win.xid != 0 {
		xproto.SetSelectionOwner(h.c, win.xid, h.atoms.clipboard, xproto.TimeCurrentTime)
	}
}

// handleSelectionRequest answers someone pasting what we copied.
func (h *Host) handleSelectionRequest(rq xproto.SelectionRequestEvent) {
	reply := xproto.SelectionNotifyEvent{
		Time:      rq.Time,
		Requestor: rq.Requestor,
		Selection: rq.Selection,
		Target:    rq.Target,
		Property:  xproto.AtomNone,
	}

	switch {
	case rq.Target == h.atoms.targets:
		data := atomBytes(h.atoms.targets, h.atoms.utf8, xproto.AtomString)
		xproto.ChangeProperty(h.c, xproto.PropModeReplace, rq.Requestor, rq.Property, xproto.AtomAtom, 32,
			uint32(len(data)/4), data) //nolint:gosec // three atoms

		reply.Property = rq.Property
	case (rq.Target == h.atoms.utf8 || rq.Target == xproto.AtomString) && h.hasClip:
		xproto.ChangeProperty(h.c, xproto.PropModeReplace, rq.Requestor, rq.Property, rq.Target, 8,
			uint32(len(h.clip)), []byte(h.clip)) //nolint:gosec // far below 4 GiB

		reply.Property = rq.Property
	}

	xproto.SendEvent(h.c, false, rq.Requestor, 0, string(reply.Bytes()))
}
