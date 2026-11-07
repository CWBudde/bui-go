// SPDX-License-Identifier: GPL-2.0-or-later

//go:build cgo && linux && amd64

// Package cgoimage is the one kit call that tests need on the native kit and that
// engine.Kit does not have: the binding adds no image API before images work on the wasm
// engine. Only tests import it. It is not in a _test.go file, as those cannot use cgo.
package cgoimage

/*
#cgo CFLAGS: -I${SRCDIR}/../../../include
#cgo LDFLAGS: -L${SRCDIR}/../../../lib/linux_amd64 -lbui_kit -lbui_gpu -lbui_core -lstdc++ -ldl -lm -lpthread
#include <stdlib.h>
#include "BUI_kit.h"
*/
import "C"

import (
	"unsafe"

	"github.com/CWBudde/bui-go/internal/engine"
)

// SetSource shows the image file at src on an "image" node, filling it.
func SetSource(n engine.Node, src string) {
	cs := C.CString(src)
	defer C.free(unsafe.Pointer(cs))

	// As in cgoengine: a Node holds a C pointer as an integer.
	node := *(**C.BKNode)(unsafe.Pointer(&n))
	C.BK_node_set_image_source(node, cs, C.BK_FIT_FILL)
}
