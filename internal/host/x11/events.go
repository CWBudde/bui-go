// SPDX-License-Identifier: GPL-2.0-or-later

package x11

import (
	"fmt"
	"os"
	"sync"
	"time"

	"github.com/jezek/xgb"
	"github.com/jezek/xgb/xproto"
)

// eventQueue holds X events until the loop asks for them, in a list with no limit. xgb
// reads replies and events from one socket, and blocks once its own queue of 500 events
// is full, so a UI thread waiting for a reply, with nobody reading events, would wait
// forever. Taking every event right away rules that out.
type eventQueue struct {
	mu     sync.Mutex
	events []xgb.Event
	closed bool
	// errors counts X errors, which tests check after requests that report no other way.
	errors int
	// notify holds at most one signal that events or the closing arrived.
	notify chan struct{}
}

// newEventQueue starts the goroutine that reads c. It ends when the connection does.
func newEventQueue(c *xgb.Conn) *eventQueue {
	q := &eventQueue{notify: make(chan struct{}, 1)}

	go func() {
		for {
			ev, err := c.WaitForEvent()

			switch {
			case err != nil:
				// Xlib's default handler ends the process here, but the host's requests
				// can lose races with the window manager and the user: warping a pointer
				// over a window that just went, for instance.
				fmt.Fprintf(os.Stderr, "bui: X error: %v\n", err)
				q.mu.Lock()
				q.errors++
				q.mu.Unlock()
			case ev == nil:
				q.close()

				return
			default:
				q.push(ev)
			}
		}
	}()

	return q
}

func (q *eventQueue) push(ev xgb.Event) {
	q.mu.Lock()
	q.events = append(q.events, ev)
	q.mu.Unlock()
	q.signal()
}

func (q *eventQueue) close() {
	q.mu.Lock()
	q.closed = true
	q.mu.Unlock()
	q.signal()
}

func (q *eventQueue) signal() {
	select {
	case q.notify <- struct{}{}:
	default:
	}
}

// pop returns the oldest event, or nil if there is none.
func (q *eventQueue) pop() xgb.Event {
	q.mu.Lock()
	defer q.mu.Unlock()

	if len(q.events) == 0 {
		return nil
	}

	ev := q.events[0]
	q.events[0] = nil
	q.events = q.events[1:]

	return ev
}

// peek returns the oldest event without removing it, waiting up to wait for one.
func (q *eventQueue) peek(wait time.Duration) xgb.Event {
	deadline := time.NewTimer(wait)
	defer deadline.Stop()

	for {
		q.mu.Lock()

		var ev xgb.Event
		if len(q.events) > 0 {
			ev = q.events[0]
		}

		closed := q.closed
		q.mu.Unlock()

		if ev != nil || closed {
			return ev
		}

		select {
		case <-q.notify:
		case <-deadline.C:
			return nil
		}
	}
}

// waitFor returns and removes the first event match accepts, waiting up to wait. It
// leaves the others in order, for the loop to handle afterwards.
func (q *eventQueue) waitFor(wait time.Duration, match func(xgb.Event) bool) xgb.Event {
	deadline := time.NewTimer(wait)
	defer deadline.Stop()

	for {
		if ev := q.take(match); ev != nil {
			return ev
		}

		select {
		case <-q.notify:
		case <-deadline.C:
			return nil
		}
	}
}

func (q *eventQueue) take(match func(xgb.Event) bool) xgb.Event {
	q.mu.Lock()
	defer q.mu.Unlock()

	for i, ev := range q.events {
		if match(ev) {
			q.events = append(q.events[:i:i], q.events[i+1:]...)

			return ev
		}
	}

	return nil
}

func (q *eventQueue) empty() bool {
	q.mu.Lock()
	defer q.mu.Unlock()

	return len(q.events) == 0
}

func (q *eventQueue) isClosed() bool {
	q.mu.Lock()
	defer q.mu.Unlock()

	return q.closed && len(q.events) == 0
}

// autorepeatWait is how long a key release waits to see whether a press follows. The
// server sends the two together, but they reach the queue one at a time.
const autorepeatWait = 2 * time.Millisecond

// isAutorepeat reports whether a release is half of an autorepeat: a press of the same
// key at the same time comes straight after it.
func (q *eventQueue) isAutorepeat(release xproto.KeyReleaseEvent) bool {
	next, ok := q.peek(autorepeatWait).(xproto.KeyPressEvent)

	return ok && next.Time == release.Time && next.Detail == release.Detail
}
