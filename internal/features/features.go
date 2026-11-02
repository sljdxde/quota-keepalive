// Package features holds product-level availability switches shared by the
// backend, scheduler, and UI projections.
package features

import "errors"

// KeepaliveAvailable is intentionally false while the provider-side
// keep-alive implementation is unstable. Existing data is retained, but the
// operation is not exposed, configurable, or executable.
const KeepaliveAvailable = false

// ErrKeepaliveDisabled reports an attempt to use the globally hidden
// keep-alive operation.
var ErrKeepaliveDisabled = errors.New("保活功能已全局停用")

// NormalizeAction maps legacy automation actions to the only action exposed
// while keep-alive is disabled. Unknown values are preserved so validation can
// still reject them.
func NormalizeAction(action string) string {
	return normalizeAction(action, KeepaliveAvailable)
}

func normalizeAction(action string, keepaliveAvailable bool) string {
	if keepaliveAvailable {
		return action
	}
	switch action {
	case "", "all", "keepalive":
		return "checkin"
	default:
		return action
	}
}

// OperationAvailable reports whether an operation may be exposed or executed.
func OperationAvailable(operation string) bool {
	return operation != "keepalive" || KeepaliveAvailable
}
