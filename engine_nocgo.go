// SPDX-License-Identifier: GPL-2.0-or-later

//go:build !(cgo && linux && amd64 && !bui_nocgo)

package bui

import (
	"github.com/CWBudde/bui-go/internal/engine"
	"github.com/CWBudde/bui-go/internal/engine/wasmengine"
)

var kit engine.Kit = wasmengine.Kit{}
