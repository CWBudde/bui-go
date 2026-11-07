// SPDX-License-Identifier: GPL-2.0-or-later

//go:build cgo && linux && amd64 && !bui_nocgo

package bui

import (
	"github.com/CWBudde/bui-go/internal/engine"
	"github.com/CWBudde/bui-go/internal/engine/cgoengine"
)

var (
	kit  engine.Kit  = cgoengine.Kit{}
	host engine.Host = cgoengine.Host{}
)
