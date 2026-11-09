// SPDX-License-Identifier: GPL-2.0-or-later

package x11

import (
	"bytes"
	"time"

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

	return win.paste()
}

// paste asks the clipboard's owner for its text. Each request names its own property, so
// that an owner answering after the timeout cannot answer this one. A refusal names no
// property, so win.late counts the requests that timed out with no answer seen since. An
// owner answers in order, so while one is outstanding, the next answer that is not ours,
// refusal or not, is the oldest late one, and the wait goes on. Late answers leave the
// queue as they come, so none stays for a later paste to take as its own.
func (win *window) paste() (string, bool) {
	h := win.host
	property := h.atoms.selections[h.selectionNext]
	h.selectionNext = (h.selectionNext + 1) % len(h.atoms.selections)

	xproto.ConvertSelection(h.c, win.xid, h.atoms.clipboard, h.atoms.utf8, property, xproto.TimeCurrentTime)

	deadline := time.Now().Add(clipboardTimeout)

	for {
		ev := h.events.waitFor(time.Until(deadline), func(ev xgb.Event) bool {
			sn, ok := ev.(xproto.SelectionNotifyEvent)

			return ok && sn.Requestor == win.xid && sn.Selection == h.atoms.clipboard
		})

		sn, ok := ev.(xproto.SelectionNotifyEvent)

		switch {
		case !ok:
			win.late++

			return "", false
		case sn.Property == property:
			// Every earlier answer came before this one.
			win.late = 0

			return win.readSelection(property)
		case win.late > 0:
			win.late--
		case sn.Property == xproto.AtomNone:
			return "", false
		}
	}
}

// readSelection takes the text the clipboard's owner stored on this window. It refuses text
// it cannot have whole: too long, or sent in parts with INCR, as owners do past about
// 256 KiB. What INCR stores is the size, not the text, and deleting the property is what
// starts the transfer, so that property stays.
func (win *window) readSelection(property xproto.Atom) (string, bool) {
	const maxClipboardLongs = 1 << 24

	head, err := xproto.GetProperty(win.host.c, false, win.xid, property, xproto.GetPropertyTypeAny, 0, 0).Reply()
	if err != nil || head == nil || head.Type == win.host.atoms.incr || head.BytesAfter > 4*maxClipboardLongs {
		return "", false
	}

	prop, err := xproto.GetProperty(win.host.c, true, win.xid, property, xproto.GetPropertyTypeAny,
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
	// An obsolete requestor names no property. ICCCM has the owner use the target then.
	if rq.Property == xproto.AtomNone {
		rq.Property = rq.Target
	}

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
	// Past one request it would take INCR. xgb truncates a request's length field rather than
	// failing, which would make the server read the rest as requests, so this refuses.
	case (rq.Target == h.atoms.utf8 || rq.Target == xproto.AtomString) && h.hasClip && len(h.clip) <= h.maxData:
		xproto.ChangeProperty(h.c, xproto.PropModeReplace, rq.Requestor, rq.Property, rq.Target, 8,
			uint32(len(h.clip)), []byte(h.clip)) //nolint:gosec // far below 4 GiB

		reply.Property = rq.Property
	}

	xproto.SendEvent(h.c, false, rq.Requestor, 0, string(reply.Bytes()))
}
