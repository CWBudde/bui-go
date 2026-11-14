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
// forever. Taking every event right away rules that out. What keeps the list short is
// that pointer motion, the one event a user can send by the thousand, replaces its
// predecessor.
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

	if n := len(q.events); n > 0 && replacesMotion(q.events[n-1], ev) {
		q.events[n-1] = ev
	} else {
		q.events = append(q.events, ev)
	}

	q.mu.Unlock()
	q.signal()
}

// replacesMotion reports whether next makes last pointless: the same window, and the same
// buttons and modifiers, whose changes a widget might care about, at a newer position.
func replacesMotion(last, next xgb.Event) bool {
	a, ok := last.(xproto.MotionNotifyEvent)
	if !ok {
		return false
	}

	b, ok := next.(xproto.MotionNotifyEvent)

	return ok && a.Event == b.Event && a.State == b.State
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

	// Let a burst's array go, as nothing else would until the next growth.
	if len(q.events) == 0 {
		q.events = nil
	}

	return ev
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

		if q.isClosed() {
			return nil
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

// hasWork reports whether the loop must not block: events wait, or the connection went.
func (q *eventQueue) hasWork() bool {
	q.mu.Lock()
	defer q.mu.Unlock()

	return len(q.events) > 0 || q.closed
}

func (q *eventQueue) isClosed() bool {
	q.mu.Lock()
	defer q.mu.Unlock()

	return q.closed && len(q.events) == 0
}
