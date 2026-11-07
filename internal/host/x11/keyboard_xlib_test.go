// SPDX-License-Identifier: GPL-2.0-or-later

//go:build cgo && linux && bui_xlibref

package x11

import (
	"os"
	"os/exec"
	"testing"

	"github.com/CWBudde/bui-go/internal/host/x11/xlibref"
	"github.com/jezek/xgb"
	"github.com/jezek/xgb/xproto"
)

// The modifier states a key is compared under, by their state bits on an XKB server
// with the usual bindings: Mod2 is Num Lock and Mod5 is AltGr.
var referenceStates = []struct {
	name  string
	state uint16
}{
	{"none", 0},
	{"shift", uint16(xproto.ModMaskShift)},
	{"caps", uint16(xproto.ModMaskLock)},
	{"shift+caps", uint16(xproto.ModMaskShift | xproto.ModMaskLock)},
	{"numlock", uint16(xproto.ModMask2)},
	{"shift+numlock", uint16(xproto.ModMaskShift | xproto.ModMask2)},
	{"altgr", uint16(xproto.ModMask5)},
	{"shift+altgr", uint16(xproto.ModMaskShift | xproto.ModMask5)},
	{"ctrl", uint16(xproto.ModMaskControl)},
	{"alt", uint16(xproto.ModMask1)},
}

func compareWithXlib(t *testing.T) {
	t.Helper()

	c, err := xgb.NewConn()
	if err != nil {
		t.Skipf("no display: %v", err)
	}
	defer c.Close()

	k, err := newKeyboard(c)
	if err != nil {
		t.Fatal(err)
	}

	d, err := xlibref.Open()
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()

	setup := xproto.Setup(c)
	compared, wrong := 0, 0

	for code := int(setup.MinKeycode); code <= int(setup.MaxKeycode); code++ {
		for _, st := range referenceStates {
			want, text := d.Lookup(byte(code), st.state)
			got := k.lookup(byte(code), st.state)
			compared++

			if got != want {
				wrong++

				t.Errorf("key %d %s: keysym %#x %q, Xlib %#x %q", code, st.name, got, keysymName(got), want, xlibref.Name(want))

				continue
			}

			if name := keysymName(got); want != 0 && name != xlibref.Name(want) {
				t.Errorf("keysym %#x: name %q, Xlib %q", got, name, xlibref.Name(want))
			}

			checkText(t, code, st.name, st.state, got, text)
		}
	}

	t.Logf("%d comparisons, %d keysyms differ", compared, wrong)
}

// checkText compares ASCII results only: Xlib's text is in the C locale's encoding,
// while the Go host always gives UTF-8, and Ctrl makes Xlib's a control code, which
// the host drops before it reaches the kit.
func checkText(t *testing.T, code int, state string, bits uint16, sym uint32, want []byte) {
	t.Helper()

	if bits&modControl != 0 || len(want) != 1 || want[0] >= 0x80 {
		return
	}

	if got := keysymText(sym); got != string(want) {
		t.Errorf("key %d %s: text %q for %q, Xlib %q", code, state, got, keysymName(sym), want)
	}
}

func TestLookupMatchesXlib(t *testing.T) { compareWithXlib(t) }

// Other layouts need setxkbmap, which changes the server for everyone, so only on a
// private display.
func TestLookupMatchesXlibOnLayouts(t *testing.T) {
	if os.Getenv("BUI_TEST_XDOTOOL") != "1" {
		t.Skip("opt-in with BUI_TEST_XDOTOOL=1, on a private display")
	}

	if _, err := exec.LookPath("setxkbmap"); err != nil {
		t.Skip("no setxkbmap")
	}

	for _, layout := range [][]string{{"us"}, {"de"}, {"de", "-variant", "nodeadkeys"}, {"fr"}, {"ru"}, {"gr"}, {"us", "-variant", "dvorak"}} {
		t.Run(layout[0]+"/"+layoutVariant(layout), func(t *testing.T) {
			if out, err := exec.Command("setxkbmap", layout...).CombinedOutput(); err != nil {
				t.Skipf("setxkbmap %v: %v\n%s", layout, err, out)
			}

			compareWithXlib(t)
		})
	}
}

func layoutVariant(layout []string) string {
	if len(layout) == 3 {
		return layout[2]
	}

	return "default"
}
