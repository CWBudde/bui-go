// SPDX-License-Identifier: GPL-2.0-or-later

package bui

import (
	"github.com/CWBudde/bui-go/internal/engine"
	"github.com/CWBudde/bui-go/internal/host/cocoa"
)

var host engine.Host = cocoa.New(kit)
