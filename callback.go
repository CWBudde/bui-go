// SPDX-License-Identifier: GPL-2.0-or-later

package bui

import "github.com/CWBudde/bui-go/internal/engine"

// nodes finds a Node from the id the kit holds for it, so that no Go pointer
// crosses into the kit. An id is reused only after its node is freed and the
// counter has wrapped. Like all kit-side state it is UI-thread only and
// intentionally unlocked, so that -race reports unserialized use.
var (
	nodes      = map[uint32]*Node{}
	lastNodeID uint32
)

// newNodeID skips 0, which means "no id", and the ids of live nodes.
func newNodeID(n *Node) uint32 {
	for {
		lastNodeID++

		if _, taken := nodes[lastNodeID]; !taken && lastNodeID != 0 {
			break
		}
	}

	nodes[lastNodeID] = n

	return lastNodeID
}

func liveHandleCount() int { return len(nodes) }

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
		host.Wake()
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

func init() {
	kit.SetEmit(handleEmit)
}

// handleEmit receives every kit event for a node with an id. A panic here ends the
// process: see engine.ExitIfUnwinding.
func handleEmit(id uint32, kind int, a float64) {
	n := nodes[id]
	if n.closing {
		return
	}

	emitDepth++

	switch kind {
	case engine.EvClick:
		if n.onClick != nil {
			n.onClick()
		}
	case engine.EvChange:
		if n.onChange != nil {
			n.onChange(a)
		}
	}

	emitDepth--
}
