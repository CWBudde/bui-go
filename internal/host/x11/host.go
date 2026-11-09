// SPDX-License-Identifier: GPL-2.0-or-later

// Package x11 is a window host in pure Go, a port of BUI's kit_window_x11.c on xgb. The
// kit renders into a raster in software, so a host only forwards input and presents
// pixels. It works with any engine.Kit.
//
// All of it runs on the UI thread except Wake and the goroutine reading X events, which
// only hands them over: see eventQueue.
package x11

import (
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/CWBudde/bui-go/internal/engine"
	"github.com/jezek/xgb"
	"github.com/jezek/xgb/xproto"
)

// The pixel layout the kit's raster has: 0xAARRGGBB words, so B, G, R, A in memory.
const (
	rasterDepth     = 24
	rasterBitsPixel = 32
	redMask         = 0xff0000
	greenMask       = 0x00ff00
	blueMask        = 0x0000ff
)

// clipboardTimeout is how long a paste waits for the clipboard's owner, as in the C host.
const clipboardTimeout = 300 * time.Millisecond

// Host implements engine.Host on one X connection. It connects at the first Supported,
// and stays connected for the life of the process, as the C host does.
type Host struct {
	kit engine.Kit

	c        *xgb.Conn
	root     xproto.Window
	visual   xproto.Visualid
	maxData  int
	keyboard *keyboard
	events   *eventQueue
	atoms    atoms
	scale    float32

	windows []*window
	lastID  engine.Window
	open    int

	cursorFont xproto.Font
	cursors    [cursorCount]xproto.Cursor

	clip    string
	hasClip bool

	// wake holds at most one pending wake, so that Wake never blocks.
	wake     chan struct{}
	wakeFunc func()
	quit     bool
	// lost says the connection broke. Nothing the host does can work any more.
	lost bool

	// frameDue paces animation: nothing renders before it. See renderPending.
	frameDue time.Time

	// putBuf holds the rows of a damage rectangle narrower than the raster.
	putBuf []byte
}

var _ engine.Host = (*Host)(nil)

// atoms are the interned names the host needs, once connected.
type atoms struct {
	delete, protocols, clipboard, utf8, targets, incr, netWMName xproto.Atom

	// selections are the properties pastes name. A paste takes one that no request still
	// waiting for its answer names, and the host interns another when all are taken. The
	// server never frees an atom, so they are reused rather than one per paste.
	selections []xproto.Atom
}

// init quiets xgb's own log, which says "could not get authority info" on every
// connection without an Xauthority file, though most then work: Xvfb needs none. What
// matters reaches the caller as ErrNoDisplay, or stderr through the event queue.
func init() {
	xgb.Logger = log.New(io.Discard, "", 0)
}

// New returns a host for kit. It does not connect yet, and it never calls kit before a
// window opens, so that importing package bui costs nothing.
func New(kit engine.Kit) *Host {
	return &Host{kit: kit, wake: make(chan struct{}, 1)}
}

// Supported implements engine.Host.
func (h *Host) Supported() bool {
	if h.lost {
		return false
	}

	if h.c != nil {
		return true
	}

	c, err := dial()
	if err != nil {
		return false
	}

	if err := h.connect(c); err != nil {
		fmt.Fprintf(os.Stderr, "bui: cannot use the X server: %v\n", err)
		c.Close()

		return false
	}

	return true
}

// connect reads what the host needs from a new connection. The event goroutine starts
// first, as it is what keeps replies flowing: see eventQueue.
func (h *Host) connect(c *xgb.Conn) error {
	events := newEventQueue(c)

	setup := xproto.Setup(c)
	screen := setup.DefaultScreen(c)

	if err := checkPixelFormat(setup, screen); err != nil {
		return err
	}

	kb, err := newKeyboard(c, enableXKB(c))
	if err != nil {
		return err
	}

	a, err := internAtoms(c)
	if err != nil {
		return err
	}

	const requestHeader = 24

	h.c, h.events, h.keyboard, h.atoms = c, events, kb, a
	h.root, h.visual = screen.Root, screen.RootVisual
	h.maxData = 4*int(setup.MaximumRequestLength) - requestHeader
	h.scale = displayScale(c, h.root)

	return nil
}

// errPixelFormat is a server whose pixels differ from the raster's.
var errPixelFormat = errors.New("unsupported pixel format")

// checkPixelFormat requires what lets the raster go to the server as it is. The C host
// assumes the same without checking.
func checkPixelFormat(setup *xproto.SetupInfo, screen *xproto.ScreenInfo) error {
	if setup.ImageByteOrder != xproto.ImageOrderLSBFirst {
		return fmt.Errorf("%w: image byte order %d is not LSB first", errPixelFormat, setup.ImageByteOrder)
	}

	if screen.RootDepth != rasterDepth {
		return fmt.Errorf("%w: root depth %d is not %d", errPixelFormat, screen.RootDepth, rasterDepth)
	}

	if !slices.ContainsFunc(setup.PixmapFormats, func(f xproto.Format) bool {
		return f.Depth == rasterDepth && f.BitsPerPixel == rasterBitsPixel
	}) {
		return fmt.Errorf("%w: depth %d has no %d-bit pixels", errPixelFormat, rasterDepth, rasterBitsPixel)
	}

	v := rootVisual(screen)
	if v == nil {
		return fmt.Errorf("%w: root visual %d not found", errPixelFormat, screen.RootVisual)
	}

	if v.Class != xproto.VisualClassTrueColor ||
		v.RedMask != redMask || v.GreenMask != greenMask || v.BlueMask != blueMask {
		return fmt.Errorf("%w: root visual is class %d with masks %#x %#x %#x",
			errPixelFormat, v.Class, v.RedMask, v.GreenMask, v.BlueMask)
	}

	return nil
}

func rootVisual(screen *xproto.ScreenInfo) *xproto.VisualInfo {
	for _, d := range screen.AllowedDepths {
		for i := range d.Visuals {
			if d.Visuals[i].VisualId == screen.RootVisual {
				return &d.Visuals[i]
			}
		}
	}

	return nil
}

func internAtoms(c *xgb.Conn) (atoms, error) {
	var a atoms

	for _, n := range []struct {
		name string
		atom *xproto.Atom
	}{
		{"WM_DELETE_WINDOW", &a.delete},
		{"WM_PROTOCOLS", &a.protocols},
		{"CLIPBOARD", &a.clipboard},
		{"UTF8_STRING", &a.utf8},
		{"TARGETS", &a.targets},
		{"INCR", &a.incr},
		{"_NET_WM_NAME", &a.netWMName},
	} {
		r, err := xproto.InternAtom(c, false, uint16(len(n.name)), n.name).Reply() //nolint:gosec // short constants
		if err != nil {
			return a, fmt.Errorf("InternAtom %s: %w", n.name, err)
		}

		*n.atom = r.Atom
	}

	selection, err := internSelection(c, 0)
	a.selections = []xproto.Atom{selection}

	return a, err
}

// internSelection interns the name of the paste property with index i.
func internSelection(c *xgb.Conn, i int) (xproto.Atom, error) {
	name := fmt.Sprintf("BUI_SELECTION_%d", i)

	r, err := xproto.InternAtom(c, false, uint16(len(name)), name).Reply() //nolint:gosec // short names
	if err != nil {
		return xproto.AtomNone, fmt.Errorf("InternAtom %s: %w", name, err)
	}

	return r.Atom, nil
}

// displayScale is BUI_UI_SCALE, or Xft.dpi as GHOST_SystemX11 reads it, else 1. The C
// host takes any BUI_UI_SCALE, but one that parses to 0 or less makes no window.
func displayScale(c *xgb.Conn, root xproto.Window) float32 {
	const (
		baseDPI       = 96
		resourceBytes = 1 << 20
	)

	if env := os.Getenv("BUI_UI_SCALE"); env != "" {
		if v, err := strconv.ParseFloat(env, 32); err == nil && v > 0 {
			return float32(v)
		}
	}

	r, err := xproto.GetProperty(c, false, root, xproto.AtomResourceManager, xproto.AtomString,
		0, resourceBytes/4).Reply()
	if err != nil || r == nil {
		return 1
	}

	dpi := resourceDPI(string(r.Value))
	if dpi < baseDPI {
		return 1
	}

	return float32(dpi / baseDPI)
}

// resourceDPI finds Xft.dpi in the RESOURCE_MANAGER property's lines, 0 for none.
func resourceDPI(resources string) float64 {
	for line := range strings.SplitSeq(resources, "\n") {
		key, value, ok := strings.Cut(line, ":")
		if !ok || strings.TrimSpace(key) != "Xft.dpi" {
			continue
		}

		if dpi, err := strconv.ParseFloat(strings.TrimSpace(value), 64); err == nil {
			return dpi
		}
	}

	return 0
}

// WakeInit implements engine.Host.
func (h *Host) WakeInit(f func()) { h.wakeFunc = f }

// Wake implements engine.Host.
func (h *Host) Wake() {
	select {
	case h.wake <- struct{}{}:
	default:
	}
}

// Quit implements engine.Host.
func (h *Host) Quit() {
	h.quit = true
	h.Wake()
}
