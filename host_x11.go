// SPDX-License-Identifier: GPL-2.0-or-later

//go:build linux && !(cgo && amd64 && !bui_nocgo)

package bui

import (
	"github.com/CWBudde/bui-go/internal/engine"
	"github.com/CWBudde/bui-go/internal/host/x11"
)

var host engine.Host = x11.New(kit)
