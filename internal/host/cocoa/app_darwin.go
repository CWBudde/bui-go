// SPDX-License-Identifier: GPL-2.0-or-later

package cocoa

import "github.com/ebitengine/purego/objc"

// NSTerminateCancel: the application decides itself when it is done.
const terminateCancel = 0

var (
	selMainMenu       = objc.RegisterName("mainMenu")
	selSetMainMenu    = objc.RegisterName("setMainMenu:")
	selSetWindowsMenu = objc.RegisterName("setWindowsMenu:")
	selProcessName    = objc.RegisterName("processName")
	selInitWithTitle  = objc.RegisterName("initWithTitle:")
	selAddItemTitled  = objc.RegisterName("addItemWithTitle:action:keyEquivalent:")
	selAddItem        = objc.RegisterName("addItem:")
	selSetKeyMask     = objc.RegisterName("setKeyEquivalentModifierMask:")
	selSeparatorItem  = objc.RegisterName("separatorItem")
	selSetSubmenu     = objc.RegisterName("setSubmenu:")
	selAppDelegate    = objc.RegisterName("delegate")
	selSetTabbing     = objc.RegisterName("setAllowsAutomaticWindowTabbing:")
)

// appDelegate is NSApp's delegate. NSApp doesn't retain it, so it is kept for good.
var appDelegate objc.ID

// registerAppDelegate makes the class of NSApp's delegate. Quitting, by Cmd+Q, the Dock or
// logging out, closes every window as its close button would, and the loop ends when none is
// left, as in the C host. AppKit's own answer would end the process from inside the loop.
func registerAppDelegate() bool {
	shouldTerminate := func(_ objc.ID, _ objc.SEL, _ objc.ID) uint64 {
		guarded(func() { current.terminate() })

		return terminateCancel
	}

	cls, err := objc.RegisterClass("BUIGoAppDelegate", objc.GetClass("NSObject"), nil, nil, []objc.MethodDef{
		{Cmd: objc.RegisterName("applicationShouldTerminate:"), Fn: shouldTerminate},
		{
			Cmd: objc.RegisterName("applicationSupportsSecureRestorableState:"),
			Fn:  func(_ objc.ID, _ objc.SEL, _ objc.ID) bool { return true },
		},
	})
	if err != nil {
		return false
	}

	appDelegate = objc.ID(cls).Send(selAlloc).Send(selInit)

	return true
}

// terminate closes every open window. With none left the loop ends.
func (h *Host) terminate() {
	for _, win := range h.windowsSnapshot() {
		if win.nswin != 0 {
			win.nswin.Send(selClose)
		}
	}

	if h.open == 0 {
		h.quit = true
		h.Wake()
	}
}

// setUpApp gives NSApp the C host's menus, unless it has a menu already, and the delegate,
// unless it has one. The caller holds a pool.
func (h *Host) setUpApp() {
	if h.app.Send(selMainMenu) == 0 {
		name := goString(clsNSProcessInfo.Send(selProcessInfo).Send(selProcessName))

		appMenu := menu(
			name,
			menuItem{"Hide " + name, "hide:", "h", flagCommand},
			menuItem{"Hide Others", "hideOtherApplications:", "h", flagOption | flagCommand},
			menuItem{"Show All", "unhideAllApplications:", "", 0},
			menuItem{},
			menuItem{"Quit " + name, "terminate:", "q", flagCommand},
		)
		windowMenu := menu(
			"Window",
			menuItem{"Minimize", "performMiniaturize:", "m", flagCommand},
			menuItem{"Zoom", "performZoom:", "", 0},
			menuItem{"Enter Full Screen", "toggleFullScreen:", "f", flagControl | flagCommand},
			menuItem{"Close", "performClose:", "w", flagCommand},
		)

		bar := clsNSMenu.Send(selAlloc).Send(selInit)

		for _, sub := range []objc.ID{appMenu, windowMenu} {
			item := clsNSMenuItem.Send(selAlloc).Send(selInit)
			item.Send(selSetSubmenu, sub)
			bar.Send(selAddItem, item)
			item.Send(selRelease)
		}

		h.app.Send(selSetMainMenu, bar)
		h.app.Send(selSetWindowsMenu, windowMenu)
		bar.Send(selRelease)
		appMenu.Send(selRelease)
		windowMenu.Send(selRelease)
	}

	if h.app.Send(selAppDelegate) == 0 {
		h.app.Send(selSetDelegate, appDelegate)
	}

	clsNSWindow.Send(selSetTabbing, false)
}

// menuItem is one item of a menu. The zero value is a separator. A mask of 0 leaves AppKit's
// default, Command.
type menuItem struct {
	title, action, key string
	mask               uint64
}

// menu returns a new menu, retained, with the items.
func menu(title string, items ...menuItem) objc.ID {
	m := clsNSMenu.Send(selAlloc).Send(selInitWithTitle, nsString(title))

	for _, it := range items {
		if it.title == "" {
			m.Send(selAddItem, clsNSMenuItem.Send(selSeparatorItem))

			continue
		}

		item := m.Send(selAddItemTitled, nsString(it.title), objc.RegisterName(it.action), nsString(it.key))
		if it.mask != 0 {
			item.Send(selSetKeyMask, it.mask)
		}
	}

	return m
}
