// SPDX-License-Identifier: GPL-2.0-or-later

//go:build !linux

package bui

import (
	"github.com/CWBudde/bui-go/internal/engine"
	"github.com/CWBudde/bui-go/internal/engine/wasmengine"
)

var host engine.Host = wasmengine.Host{}
