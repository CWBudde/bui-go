// SPDX-License-Identifier: GPL-2.0-or-later

//go:build !linux

package main

import bui "github.com/CWBudde/bui-go"

// Elsewhere, package bui's own window shows the tree: Cocoa on macOS, none on Windows.
func newWindow(root *bui.Node, title string, w, h int) (window, error) {
	return bui.NewWindow(root, title, w, h) //nolint:wrapcheck // the example passes it on
}

var (
	run  = bui.Run
	quit = bui.Quit
)
