//go:build darwin && !ios

package gui

/*
#cgo CFLAGS: -x objective-c
#cgo LDFLAGS: -framework Cocoa

#import <Cocoa/Cocoa.h>

static void setQKADockVisible(bool visible) {
	dispatch_async(dispatch_get_main_queue(), ^{
		[NSApp setActivationPolicy:visible
			? NSApplicationActivationPolicyRegular
			: NSApplicationActivationPolicyAccessory];
	});
}
*/
import "C"

func setDockIconVisible(visible bool) {
	C.setQKADockVisible(C.bool(visible))
}
