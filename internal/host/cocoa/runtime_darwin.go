// SPDX-License-Identifier: GPL-2.0-or-later

package cocoa

import (
	"runtime"
	"sync"
	"unsafe"

	"github.com/ebitengine/purego"
	"github.com/ebitengine/purego/objc"
)

// Geometry as AppKit passes it: CGFloat is a double on every Mac Go supports.
type (
	nsPoint struct{ X, Y float64 }
	nsSize  struct{ W, H float64 }
	nsRect  struct {
		Origin nsPoint
		Size   nsSize
	}
)

// The C functions the host calls, bound by load.
var (
	cfDataCreate            func(alloc uintptr, data *byte, n int) uintptr
	cfRelease               func(uintptr)
	cgColorSpaceCreateNamed func(name uintptr) uintptr
	cgDataProviderFromData  func(data uintptr) uintptr
	cgDataProviderRelease   func(uintptr)
	cgImageCreate           func(w, h, bitsComponent, bitsPixel, stride int, space uintptr, info uint32,
		provider, decode uintptr, interpolate bool, intent int32) uintptr
	cgImageRelease   func(uintptr)
	cgSessionCurrent func() uintptr
	cgWarp           func(nsPoint) int32
	cgAssociateMouse func(bool) int32
	pthreadMainNP    func() int32
	poolPush         func() uintptr
	poolPop          func(uintptr)
)

// Selectors are cached, as registering one takes the runtime's global lock.
var (
	selAlloc               = objc.RegisterName("alloc")
	selInit                = objc.RegisterName("init")
	selRelease             = objc.RegisterName("release")
	selRetain              = objc.RegisterName("retain")
	selSharedApplication   = objc.RegisterName("sharedApplication")
	selSetActivationPolicy = objc.RegisterName("setActivationPolicy:")
	selFinishLaunching     = objc.RegisterName("finishLaunching")
	selNextEvent           = objc.RegisterName("nextEventMatchingMask:untilDate:inMode:dequeue:")
	selSendEvent           = objc.RegisterName("sendEvent:")
	selPostEvent           = objc.RegisterName("postEvent:atStart:")
	selOtherEvent          = objc.RegisterName(
		"otherEventWithType:location:modifierFlags:timestamp:windowNumber:context:subtype:data1:data2:",
	)
	selType              = objc.RegisterName("type")
	selSubtype           = objc.RegisterName("subtype")
	selDistantFuture     = objc.RegisterName("distantFuture")
	selDateSinceNow      = objc.RegisterName("dateWithTimeIntervalSinceNow:")
	selStringUTF8        = objc.RegisterName("stringWithUTF8String:")
	selInitWindow        = objc.RegisterName("initWithContentRect:styleMask:backing:defer:")
	selSetReleased       = objc.RegisterName("setReleasedWhenClosed:")
	selSetTitle          = objc.RegisterName("setTitle:")
	selSetDelegate       = objc.RegisterName("setDelegate:")
	selSetContentView    = objc.RegisterName("setContentView:")
	selContentView       = objc.RegisterName("contentView")
	selCenter            = objc.RegisterName("center")
	selOrderFront        = objc.RegisterName("orderFrontRegardless")
	selClose             = objc.RegisterName("close")
	selPerformClose      = objc.RegisterName("performClose:")
	selBackingScale      = objc.RegisterName("backingScaleFactor")
	selMainScreen        = objc.RegisterName("mainScreen")
	selBounds            = objc.RegisterName("bounds")
	selObject            = objc.RegisterName("object")
	selInitFrame         = objc.RegisterName("initWithFrame:")
	selSetLayer          = objc.RegisterName("setLayer:")
	selSetWantsLayer     = objc.RegisterName("setWantsLayer:")
	selSetContents       = objc.RegisterName("setContents:")
	selSetContentsScale  = objc.RegisterName("setContentsScale:")
	selSetContentsGrav   = objc.RegisterName("setContentsGravity:")
	selBegin             = objc.RegisterName("begin")
	selCommit            = objc.RegisterName("commit")
	selFlush             = objc.RegisterName("flush")
	selSetDisableActions = objc.RegisterName("setDisableActions:")
)

// Classes, found by load.
var (
	clsNSApplication, clsNSEvent, clsNSDate, clsNSString, clsNSWindow,
	clsNSScreen, clsCALayer, clsCATransaction,
	clsNSCursor, clsNSPasteboard, clsNSArray, clsNSProcessInfo, clsNSTrackingArea objc.ID
	nsDefaultRunLoopMode objc.ID
	srgbSpace            uintptr
	delegateClass        objc.Class
)

var (
	loadOnce sync.Once
	loaded   bool
)

// load opens the frameworks at first use, so that a headless program never does.
func load() bool {
	loadOnce.Do(func() { loaded = loadNow() })

	return loaded
}

func loadNow() bool {
	if !bindFunctions() {
		return false
	}

	cls := func(name string) objc.ID { return objc.ID(objc.GetClass(name)) }
	clsNSApplication, clsNSEvent = cls("NSApplication"), cls("NSEvent")
	clsNSDate, clsNSString = cls("NSDate"), cls("NSString")
	clsNSWindow, clsNSScreen = cls("NSWindow"), cls("NSScreen")
	clsCALayer, clsCATransaction = cls("CALayer"), cls("CATransaction")
	clsNSCursor, clsNSPasteboard = cls("NSCursor"), cls("NSPasteboard")
	clsNSArray, clsNSProcessInfo = cls("NSArray"), cls("NSProcessInfo")
	clsNSTrackingArea = cls("NSTrackingArea")

	pool := poolPush()
	defer poolPop(pool)

	// Kept for good. The colour space's name is an NSString with this content, which is
	// all that CGColorSpaceCreateWithName compares.
	nsDefaultRunLoopMode = nsString("kCFRunLoopDefaultMode").Send(selRetain)
	srgbSpace = cgColorSpaceCreateNamed(uintptr(nsString("kCGColorSpaceSRGB")))

	return srgbSpace != 0 && registerDelegate() && registerView()
}

// bindFunctions opens the frameworks and binds the C functions.
func bindFunctions() bool {
	for _, path := range []string{
		"/System/Library/Frameworks/AppKit.framework/AppKit",
		"/System/Library/Frameworks/QuartzCore.framework/QuartzCore",
		"/System/Library/Frameworks/CoreGraphics.framework/CoreGraphics",
		"/System/Library/Frameworks/CoreFoundation.framework/CoreFoundation",
		"/usr/lib/libobjc.A.dylib",
		"/usr/lib/libSystem.B.dylib",
	} {
		if _, err := purego.Dlopen(path, purego.RTLD_NOW|purego.RTLD_GLOBAL); err != nil {
			return false
		}
	}

	for name, fn := range map[string]any{
		"CFDataCreate":                           &cfDataCreate,
		"CFRelease":                              &cfRelease,
		"CGColorSpaceCreateWithName":             &cgColorSpaceCreateNamed,
		"CGDataProviderCreateWithCFData":         &cgDataProviderFromData,
		"CGDataProviderRelease":                  &cgDataProviderRelease,
		"CGImageCreate":                          &cgImageCreate,
		"CGImageRelease":                         &cgImageRelease,
		"CGSessionCopyCurrentDictionary":         &cgSessionCurrent,
		"CGWarpMouseCursorPosition":              &cgWarp,
		"CGAssociateMouseAndMouseCursorPosition": &cgAssociateMouse,
		"pthread_main_np":                        &pthreadMainNP,
		"objc_autoreleasePoolPush":               &poolPush,
		"objc_autoreleasePoolPop":                &poolPop,
	} {
		p, err := purego.Dlsym(purego.RTLD_DEFAULT, name)
		if err != nil {
			return false
		}

		purego.RegisterFunc(fn, p)
	}

	return true
}

// nsString returns an autoreleased NSString. The caller needs a pool, or leaks it.
func nsString(s string) objc.ID {
	b := append([]byte(s), 0)
	id := clsNSString.Send(selStringUTF8, unsafe.Pointer(&b[0]))
	runtime.KeepAlive(b)

	return id
}
