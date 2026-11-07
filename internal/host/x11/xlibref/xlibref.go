// SPDX-License-Identifier: GPL-2.0-or-later

//go:build cgo && linux && bui_xlibref

// Package xlibref calls libX11, as the reference for the Go keyboard translation. It
// needs libX11's headers, which nothing else in a build does, so it has its own tag:
//
//	xvfb-run -a go test -tags bui_xlibref ./internal/host/x11/
package xlibref

/*
#cgo LDFLAGS: -lX11
#include <stdlib.h>
#include <string.h>
#include <X11/Xlib.h>
#include <X11/Xutil.h>

static unsigned long lookup(Display *d, int code, unsigned state, char *text, int size, int *len)
{
  XKeyEvent ev;
  memset(&ev, 0, sizeof(ev));
  ev.type = KeyPress;
  ev.display = d;
  ev.keycode = code;
  ev.state = state;
  ev.root = ev.window = DefaultRootWindow(d);
  KeySym sym = 0;
  *len = XLookupString(&ev, text, size, &sym, NULL);
  return sym;
}
*/
import "C"

import (
	"errors"
	"unsafe"
)

// Display is an Xlib connection.
type Display struct{ d *C.Display }

// Open connects to $DISPLAY.
func Open() (*Display, error) {
	d := C.XOpenDisplay(nil)
	if d == nil {
		return nil, errors.New("XOpenDisplay failed")
	}

	return &Display{d: d}, nil
}

// Close disconnects.
func (d *Display) Close() { C.XCloseDisplay(d.d) }

// Lookup is XLookupString on a synthetic key press: the keysym and the bytes of the
// text, which are in the C locale's encoding.
func (d *Display) Lookup(code byte, state uint16) (sym uint32, text []byte) {
	var buf [64]C.char

	var n C.int

	s := C.lookup(d.d, C.int(code), C.uint(state), &buf[0], C.int(len(buf)), &n)

	return uint32(s), C.GoBytes(unsafe.Pointer(&buf[0]), n)
}

// Name is XKeysymToString.
func Name(sym uint32) string {
	return C.GoString(C.XKeysymToString(C.KeySym(sym)))
}
