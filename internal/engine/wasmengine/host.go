// SPDX-License-Identifier: GPL-2.0-or-later

package wasmengine

import "github.com/CWBudde/bui-go/internal/engine"

// Host is the window system of a build without one yet: windows are unsupported,
// so package bui's Run reports ErrNoDisplay. It never touches the module, so its
// calls cost nothing, and Phase 3 replaces it with a Go host.
type Host struct{}

var _ engine.Host = Host{}

// Supported implements engine.Host.
func (Host) Supported() bool { return false }

// WindowNew implements engine.Host.
func (Host) WindowNew(engine.Context, string, int, int) engine.Window { return 0 }

// WindowShow implements engine.Host.
func (Host) WindowShow(engine.Window) {}

// WindowFree implements engine.Host.
func (Host) WindowFree(engine.Window) {}

// WakeInit implements engine.Host.
func (Host) WakeInit(func()) {}

// Wake implements engine.Host.
func (Host) Wake() {}

// Run implements engine.Host.
func (Host) Run() {}

// Quit implements engine.Host.
func (Host) Quit() {}
