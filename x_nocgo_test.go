// SPDX-License-Identifier: GPL-2.0-or-later

//go:build !(cgo && linux && amd64 && !bui_nocgo)

package bui

import (
	"os"
	"os/exec"
	"testing"
)

// xKeysInto presses keys by their names, as xdotool's "key" takes them.
func xKeysInto(t *testing.T, title string, keys []string) string {
	t.Helper()

	return xInputInto(t, title, func() {
		xdotool(t, append([]string{"key", "--delay", "20"}, keys...)...)
	})
}

// Text beyond ASCII from real keys of a real layout, with Shift and AltGr. Only for the Go
// host, which always gives UTF-8: the C host's XLookupString answers in the locale's
// encoding, and nothing sets the locale.
//
// The layout is set rather than left to xdotool, which for a key not on the layout remaps
// a spare keycode per character: a client reading the keymap can then race the next remap,
// libX11 as well, and no real keyboard does that.
func TestXTypingOnAGermanLayout(t *testing.T) {
	if os.Getenv("BUI_TEST_XDOTOOL") != "1" {
		t.Skip("opt-in with BUI_TEST_XDOTOOL=1, on a private display")
	}

	if out, err := exec.Command("setxkbmap", "de", "-variant", "nodeadkeys").CombinedOutput(); err != nil {
		t.Skipf("setxkbmap: %v\n%s", err, out)
	}

	t.Cleanup(func() { _ = exec.Command("setxkbmap", "us").Run() })

	const want = "äÜß@€"

	if got := xKeysInto(t, "bui-go x german", []string{
		"adiaeresis", "Udiaeresis", "ssharp", "ISO_Level3_Shift+q", "ISO_Level3_Shift+e",
	}); got != want {
		t.Errorf("typed %q, want %q", got, want)
	}
}
