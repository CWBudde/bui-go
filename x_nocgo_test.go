// SPDX-License-Identifier: GPL-2.0-or-later

//go:build !(cgo && linux && amd64 && !bui_nocgo)

package bui

import "testing"

// Text beyond ASCII. Only for the Go host, which always gives UTF-8: the C host's
// XLookupString answers in the locale's encoding, and nothing sets the locale.
func TestXTypingBeyondASCII(t *testing.T) {
	if got := xTypeInto(t, "bui-go x unicode", "äöü€é"); got != "äöü€é" {
		t.Errorf("typed %q", got)
	}
}
