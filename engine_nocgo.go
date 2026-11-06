// SPDX-License-Identifier: GPL-2.0-or-later

//go:build !(cgo && linux && amd64 && !bui_nocgo)

package bui

import "github.com/CWBudde/bui-go/internal/engine"

// Phase 2 of PLAN.md selects the wasm engine here. Until then this fails the build
// on purpose, as a build without cgo always has, rather than panicking at run time.
var _ = wasmEngineArrivesInPhase2

var (
	kit  engine.Kit
	host engine.Host
)
