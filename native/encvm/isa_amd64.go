//go:build amd64

package encvm

import "sync"

// isaLock protects ISA selection state. Once locked is set to true,
// SetISA returns ErrISALocked.
var isaLock struct {
	sync.Mutex
	locked bool
}

// setISAImpl is the amd64 implementation of SetISA.
func setISAImpl(isa ISA) error {
	isaLock.Lock()
	defer isaLock.Unlock()

	if isaLock.locked {
		return ErrISALocked
	}

	switch isa {
	case ISADefault, ISASSE42:
		applySSE42()
	case ISAAutoDetect:
		applyAutoDetect()
	case ISAAVX2:
		if !hasAVX2 {
			return ErrUnsupportedISA
		}
		applyAVX2()
	case ISAAVX512:
		if !hasAVX512 {
			return ErrUnsupportedISA
		}
		applyAVX512()
	default:
		return ErrUnsupportedISA
	}
	return nil
}

// lockISAImpl freezes the ISA on amd64.
func lockISAImpl() {
	isaLock.Lock()
	isaLock.locked = true
	isaLock.Unlock()
}

// resetISAStateForTest resets all ISA selection state so that each test case
// starts from a clean slate. Must only be called from tests.
func resetISAStateForTest() {
	isaLock.Lock()
	isaLock.locked = false
	isaLock.Unlock()
	isaFirstUse = sync.Once{}
	applySSE42() // restore default
}
