// dispatchYield handles the parser suspension points that still require Go
// work, such as allocations and error reporting. By the time it runs, the
// reactor has already recorded the pending field or index plus any raw token
// data, and the handler is responsible for advancing past the yielded value.

package ndec

import "fmt"

// dispatchYield routes by userData.PendingAction. Returns nil on success;
// a non-nil error aborts Unmarshal.
func (d *driverState) dispatchYield() error {
	// Sync d.kvBuf's len to the position the C-side fast path has advanced.
	// The C-side BEGIN_MAP fast path bumps userData.KvBufLen but does not
	// touch Go's d.kvBuf header. On the yield path, Go-side reserveMapKVBuf
	// and shrink depend on len(d.kvBuf) reflecting actual usage, so every
	// yield entry must re-align.
	if d.userData.KvBufCap > 0 {
		d.kvBuf = d.kvBuf[:d.userData.KvBufLen]
	}
	action := yieldAction(d.userData.PendingAction)
	switch action {
	case yaBeginPtr:
		return d.handleBeginPtrYield()
	case yaGrowSlice:
		return d.handleGrowSliceYield()
	case yaGrowSliceStruct:
		return d.handleGrowSliceStructYield()
	case yaBeginMap:
		return d.handleBeginMapYield()
	case yaFlushMap:
		return d.handleFlushMapYield()
	case yaBeginPtrMapValue:
		return d.handleBeginPtrMapValueYield()
	case yaBase64Slice:
		return d.handleBase64Slice()
	case yaGrowSlicePtrStruct:
		return d.handleGrowSlicePtrStruct()
	case yaTypeMismatch:
		return d.makeTypeError()
	case yaUnknownField:
		return d.makeUnknownFieldError()
	default:
		return fmt.Errorf("ndec: yield action %d not supported", action)
	}
}
