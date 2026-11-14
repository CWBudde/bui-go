// SPDX-License-Identifier: GPL-2.0-or-later

package main

import (
	bui "github.com/CWBudde/bui-go"
	"github.com/CWBudde/bui-go/x11"
)

// On Linux, package x11 shows the tree, on either engine.
func newWindow(root *bui.Node, title string, w, h int) (window, error) {
	return x11.NewWindow(root, title, w, h) //nolint:wrapcheck // the example passes it on
}

var (
	run  = x11.Run
	quit = x11.Quit
)
