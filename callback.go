// SPDX-License-Identifier: GPL-2.0-or-later

package bui

/*
#include <stdint.h>
#include "bridge.h"
*/
import "C"

import (
	"fmt"
	"os"
	"runtime/cgo"
	"runtime/debug"
)

// liveHandles counts Node handles. Like all kit-side state it is UI-thread only
// and intentionally a plain int, so that -race reports unserialized use.
var liveHandles int

func newHandle(n *Node) cgo.Handle {
	liveHandles++
	return cgo.NewHandle(n)
}

// nodeOf resolves a handle made by newHandle. Handles only ever hold *Node, so
// a failed assertion is a bug and panics.
func nodeOf[H ~uintptr | ~uint64](h H) *Node {
	return cgo.Handle(h).Value().(*Node) //nolint:forcetypeassert // see above
}

func deleteHandle(h cgo.Handle) {
	h.Delete()
	liveHandles--
}

func liveHandleCount() int { return liveHandles }

// emitDepth is how many kit events are being delivered on the stack. Non-zero
// means a kit call is on the stack below the running handler.
var emitDepth int

// deferred holds destruction requested inside a handler, in request order.
var deferred []func()

// closeOrDefer queues a destruction and runs the queue unless called from a
// handler: freeing nodes or contexts while the kit is delivering an event is
// use-after-free. It always queues, so that a free cannot overtake earlier ones
// and break the order window, context, root.
func closeOrDefer(free func()) {
	deferred = append(deferred, free)
	if emitDepth == 0 {
		runDeferred()
		return
	}
	// A handler fired by an X event has no binding call to flush after it.
	if inLoop {
		C.BK_window_wake()
	}
}

// runDeferred runs queued destruction if no kit call is on the stack. The
// outermost emit returning is not enough, as that is still inside BK_press, so
// this is called after the kit call that emitted has itself returned.
func runDeferred() {
	if emitDepth > 0 {
		return
	}
	for len(deferred) > 0 {
		free := deferred[0]
		deferred = deferred[1:]
		free()
	}
}

// exitIfUnwinding guards Go code called from C. A panic or Goexit escaping it
// would abandon the C frames below, leaving the kit's state corrupt (input scope
// open, pointer grab held), and a recover further up would let the process carry
// on with that. So it ends the process, whatever callers do. It must be deferred
// directly, as recover only works there.
func exitIfUnwinding(returned *bool) {
	if *returned {
		return
	}
	fmt.Fprintf(os.Stderr, "bui: panic or Goexit in a callback from the kit: %v\n\n%s", recover(), debug.Stack())
	os.Exit(2)
}

// buiGoEmit receives every kit event for a node with a handle.
//
//export buiGoEmit
func buiGoEmit(h C.uintptr_t, kind C.int, a C.double) {
	returned := false
	defer exitIfUnwinding(&returned)
	n := nodeOf(h)
	if n.closing {
		returned = true
		return
	}
	emitDepth++
	switch kind {
	case C.BK_EV_CLICK:
		if n.onClick != nil {
			n.onClick()
		}
	case C.BK_EV_CHANGE:
		if n.onChange != nil {
			n.onChange(float64(a))
		}
	}
	emitDepth--
	returned = true
}
