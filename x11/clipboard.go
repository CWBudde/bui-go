// SPDX-License-Identifier: GPL-2.0-or-later

package x11

import (
	"bytes"
	"slices"
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

// lateHorizon is how long a paste that timed out still waits for its answer. Owners answer
// within milliseconds or never, and the horizon bounds the properties the host interns.
const lateHorizon = 5 * time.Second

// lateRequest is a paste that timed out: the property it named and when it asked.
type lateRequest struct {
	property xproto.Atom
	asked    time.Time
}

// paste asks the clipboard's owner for its text. An owner answers in order, and an answer
// names the request's property, or none for a refusal. So each paste names a property that
// no request in win.late names, and an answer naming one of those is late text. A refusal
// while win.late holds requests is the oldest one's. The host interns another property
// when all are taken, and drops requests past lateHorizon.
func (win *window) paste() (string, bool) {
	h := win.host
	now := time.Now()

	property, ok := win.freeProperty(now)
	if !ok {
		return "", false
	}

	xproto.ConvertSelection(h.c, win.xid, h.atoms.clipboard, h.atoms.utf8, property, xproto.TimeCurrentTime)

	deadline := now.Add(clipboardTimeout)

	for {
		ev := h.events.waitFor(time.Until(deadline), func(ev xgb.Event) bool {
			sn, ok := ev.(xproto.SelectionNotifyEvent)

			return ok && sn.Requestor == win.xid && sn.Selection == h.atoms.clipboard
		})

		sn, ok := ev.(xproto.SelectionNotifyEvent)
		if !ok {
			win.late = append(win.late, lateRequest{property: property, asked: now})

			return "", false
		}

		switch {
		case sn.Property == property:
			// Every earlier answer came before this one.
			win.late = nil

			return win.readSelection(property)
		case win.takeLate(sn.Property):
		case sn.Property == xproto.AtomNone:
			return "", false
		}
	}
}

// takeLate says whether an answer naming property is a late request's, and takes that
// request out of win.late with every one before it, as those were answered first.
func (win *window) takeLate(property xproto.Atom) bool {
	i := 0
	if property != xproto.AtomNone {
		i = slices.IndexFunc(win.late, func(r lateRequest) bool { return r.property == property })
	}

	if i < 0 || i >= len(win.late) {
		return false
	}

	win.late = win.late[i+1:]

	return true
}

// freeProperty drops the requests in win.late past lateHorizon, then finds a paste property
// that none of the rest names, and interns a new one when all are taken.
func (win *window) freeProperty(now time.Time) (xproto.Atom, bool) {
	h := win.host

	for len(win.late) > 0 && now.Sub(win.late[0].asked) > lateHorizon {
		win.late = win.late[1:]
	}

	for _, p := range h.atoms.selections {
		if !slices.ContainsFunc(win.late, func(r lateRequest) bool { return r.property == p }) {
			return p, true
		}
	}

	p, err := internSelection(h.c, len(h.atoms.selections))
	if err != nil {
		return xproto.AtomNone, false
	}

	h.atoms.selections = append(h.atoms.selections, p)

	return p, true
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
