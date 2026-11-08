// SPDX-License-Identifier: GPL-2.0-or-later

package cocoa

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"sync/atomic"
	"testing"
	"time"
	"unsafe"

	"github.com/ebitengine/purego"
	"github.com/ebitengine/purego/objc"
)

var (
	selItemArray       = objc.RegisterName("itemArray")
	selCount           = objc.RegisterName("count")
	selObjectAtIndex   = objc.RegisterName("objectAtIndex:")
	selSubmenu         = objc.RegisterName("submenu")
	selTitle           = objc.RegisterName("title")
	selAction          = objc.RegisterName("action")
	selKeyEquivalent   = objc.RegisterName("keyEquivalent")
	selKeyEquivMask    = objc.RegisterName("keyEquivalentModifierMask")
	selIsSeparator     = objc.RegisterName("isSeparatorItem")
	selWindowsMenu     = objc.RegisterName("windowsMenu")
	selTerminate       = objc.RegisterName("terminate:")
	selProcessDesc     = objc.RegisterName("currentProcessDescriptor")
	selAppleEventWith  = objc.RegisterName("appleEventWithEventClass:eventID:targetDescriptor:returnID:transactionID:")
	selAEDesc          = objc.RegisterName("aeDesc")
	selSharedAEManager = objc.RegisterName("sharedAppleEventManager")
	selDispatchRawAE   = objc.RegisterName("dispatchRawAppleEvent:withRawReply:handlerRefCon:")
)

// quitDone is what a child prints once the loop has ended and the windows are closed, so
// that a process AppKit ended with exit status 0 doesn't pass.
const quitDone = "quit handled, still running"

// menuEntry is one item: its title, action and key equivalent with its modifiers.
type menuEntry struct {
	title  string
	action objc.SEL
	key    string
	mask   uint64
}

func (e menuEntry) String() string {
	return fmt.Sprintf("%q %#x %q %#x", e.title, uintptr(e.action), e.key, e.mask)
}

func menuEntries(menu objc.ID) []menuEntry {
	items := menu.Send(selItemArray)
	n := objc.Send[uint64](items, selCount)
	entries := make([]menuEntry, 0, n)

	for i := range n {
		item := items.Send(selObjectAtIndex, i)
		if objc.Send[bool](item, selIsSeparator) {
			entries = append(entries, menuEntry{title: "-"})

			continue
		}

		entries = append(entries, menuEntry{
			title:  goString(item.Send(selTitle)),
			action: objc.Send[objc.SEL](item, selAction),
			key:    goString(item.Send(selKeyEquivalent)),
			mask:   objc.Send[uint64](item, selKeyEquivMask),
		})
	}

	return entries
}

// inOrder reports whether got holds every entry of want, in that order. AppKit adds items of
// its own to the Window menu, such as Close All with Option.
func inOrder(got, want []menuEntry) bool {
	for _, e := range got {
		if len(want) > 0 && e == want[0] {
			want = want[1:]
		}
	}

	return len(want) == 0
}

// The menu bar has the C host's application and Window menus. Their Close and Minimize act
// on the key window, which a test app can't have, so only a hand checks them.
func TestMenu(t *testing.T) {
	requireDisplay(t)

	onMain(func() {
		f := newFixture()
		defer f.free()

		id := f.h.WindowNew(f.ctx, "bui cocoa test", 200, 100)
		defer f.h.WindowFree(id)

		pool := poolPush()
		defer poolPop(pool)

		bar := f.h.app.Send(selMainMenu)
		if bar == 0 {
			t.Error("no main menu")

			return
		}

		items := bar.Send(selItemArray)
		if n := objc.Send[uint64](items, selCount); n != 2 {
			t.Errorf("%d menus, want 2", n)

			return
		}

		name := goString(clsNSProcessInfo.Send(selProcessInfo).Send(selProcessName))
		sel := objc.RegisterName

		for i, want := range [][]menuEntry{
			{
				{"Hide " + name, sel("hide:"), "h", flagCommand},
				{"Hide Others", sel("hideOtherApplications:"), "h", flagOption | flagCommand},
				{"Show All", sel("unhideAllApplications:"), "", flagCommand},
				{title: "-"},
				{"Quit " + name, sel("terminate:"), "q", flagCommand},
			},
			{
				{"Minimize", sel("performMiniaturize:"), "m", flagCommand},
				{"Zoom", sel("performZoom:"), "", flagCommand},
				{"Enter Full Screen", sel("toggleFullScreen:"), "f", flagControl | flagCommand},
				{"Close", sel("performClose:"), "w", flagCommand},
			},
		} {
			menu := items.Send(selObjectAtIndex, uint64(i)).Send(selSubmenu)
			if got := menuEntries(menu); !inOrder(got, want) {
				t.Errorf("menu %d:\n got %v\nwant %v", i, got, want)
			}

			if i == 1 && f.h.app.Send(selWindowsMenu) != menu {
				t.Error("the Window menu isn't NSApp's windows menu")
			}
		}
	})
}

// runUntilClosed has the loop run act once, and then run until it ends. It reports whether
// a Quit from the test had to end it.
func (f *inputFixture) runUntilClosed(act func()) bool {
	var rescued atomic.Bool

	acted := false

	f.h.WakeInit(func() {
		if !acted {
			acted = true

			pool := poolPush()
			defer poolPop(pool)

			act()
		}
	})

	timer := time.AfterFunc(5*time.Second, func() {
		rescued.Store(true)
		f.h.Quit()
	})
	defer timer.Stop()

	f.h.Wake()

	if err := f.h.Run(); err != nil {
		f.t.Error(err)
	}

	return rescued.Load()
}

// Quitting the application, by Cmd+Q, by terminate: as the Dock and the menu send it, or by
// the Apple Event that logging out sends, closes every window and ends Run. AppKit's own
// answer would end the process, so each runs in a child.
func TestQuitClosesEveryWindow(t *testing.T) {
	requireDisplay(t)

	for _, how := range []string{"cmd-q", "terminate", "apple-event"} {
		t.Run(how, func(t *testing.T) {
			cmd := exec.Command(os.Args[0], "-test.run=^TestChildProcess$", "-test.count=1", "-test.v")

			cmd.Env = append(os.Environ(), childEnv+"=quit-"+how)

			out, err := cmd.CombinedOutput()
			if err != nil || !bytes.Contains(out, []byte(quitDone)) {
				t.Errorf("child: %v\n%s", err, out)
			}
		})
	}
}

// quitChild opens two windows and quits the way how says.
func quitChild(t *testing.T, how string) {
	t.Helper()

	onMain(func() {
		f := openInput(t, "button", 100, 40)
		other := newFixture()

		defer other.free()

		otherID := f.h.WindowNew(other.ctx, "bui cocoa test", 100, 40)
		f.h.WindowShow(otherID)

		rescued := f.runUntilClosed(func() {
			switch how {
			case "quit-cmd-q":
				f.h.app.Send(selPostEvent, f.keyEvent(eventKeyDown, keyQ, "q", "q", flagCommand), false)
			case "quit-terminate":
				f.h.app.Send(selTerminate, objc.ID(0))
			case "quit-apple-event":
				dispatchQuitEvent(t)
			default:
				t.Errorf("unknown child %q", how)
			}
		})

		if rescued || f.h.open != 0 {
			t.Errorf("windows still open: %d, rescued %v", f.h.open, rescued)
		}

		f.h.WindowFree(otherID)
		f.close()
	})

	if !t.Failed() {
		fmt.Println(quitDone)
	}
}

// dispatchQuitEvent hands the process the Apple Event that the Dock's Quit and logging out
// send. It goes to the registered handler directly, so no other process is involved.
func dispatchQuitEvent(t *testing.T) {
	t.Helper()

	const (
		classAEVT = 'a'<<24 | 'e'<<16 | 'v'<<8 | 't'
		idQuit    = 'q'<<24 | 'u'<<16 | 'i'<<8 | 't'
	)

	target := objc.ID(objc.GetClass("NSAppleEventDescriptor")).Send(selProcessDesc)
	ev := objc.ID(objc.GetClass("NSAppleEventDescriptor")).Send(selAppleEventWith,
		uint32(classAEVT), uint32(idQuit), target, int16(-1), int32(0))

	// An empty AEDesc for the reply: a type and a handle.
	var reply [2]uintptr

	desc := objc.Send[unsafe.Pointer](ev, selAEDesc)
	manager := objc.ID(objc.GetClass("NSAppleEventManager")).Send(selSharedAEManager)

	if err := objc.Send[int16](manager, selDispatchRawAE, desc, unsafe.Pointer(&reply[0]), uintptr(0)); err != 0 {
		t.Errorf("dispatching the quit event: OSErr %d", err)
	}

	runtime.KeepAlive(&reply)

	if reply[1] != 0 {
		if dispose, err := purego.Dlsym(purego.RTLD_DEFAULT, "AEDisposeDesc"); err == nil {
			purego.SyscallN(dispose, uintptr(unsafe.Pointer(&reply[0])))
		}
	}
}
