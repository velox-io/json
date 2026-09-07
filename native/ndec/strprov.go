package ndec

import "unsafe"

// BindStrProvEntry is one retired string-arena generation of the current
// streaming parse: the backing a growth displaced, with the offset interval
// of the bytes this parse produced in it. Published string headers never
// move, so a discriminator bound before a growth stays in the retired
// backing, and poly case selection consults the entry to prove the parse
// published it. The layout matches BindStrProv in machine.h.
type BindStrProvEntry struct {
	Base  unsafe.Pointer // off 0  retired backing base
	Start uint32         // off 8  first offset of this parse's generation
	End   uint32         // off 12  used extent at retirement
}

// Compile-time guard that the mirror matches the 16-byte C entry.
var _ [1]struct{} = [unsafe.Sizeof(BindStrProvEntry{}) - 15]struct{}{}

// StrProvCount reports the retired-generation count of the current parse.
func (m *BindMachine) StrProvCount() uint32 {
	return *(*uint32)(unsafe.Add(unsafe.Pointer(m), BindMachineStrProvCountOffset))
}

// setStrProvCount sets the retired-generation count. It is unexported because
// lowering the count without nil-ing the dropped entries leaves a retired base
// in the noscan machine block; TruncateStrProv is the only safe way down.
func (m *BindMachine) setStrProvCount(n uint32) {
	*(*uint32)(unsafe.Add(unsafe.Pointer(m), BindMachineStrProvCountOffset)) = n
}

// TruncateStrProv drops entries at or above n, nil-ing each retired base
// before the count moves. Callers must invoke it while the dropped entries'
// backings are still rooted, before the release that drops them: the machine
// block is pooled noscan memory, and a later append's pointer store would
// otherwise capture a dead base as the write-barrier old value.
func (m *BindMachine) TruncateStrProv(n uint32) {
	cnt := m.StrProvCount()
	if cnt <= n {
		return
	}
	for i := n; i < cnt; i++ {
		entry := (*BindStrProvEntry)(unsafe.Add(unsafe.Pointer(m),
			BindMachineStrProvOffset+uintptr(i)*unsafe.Sizeof(BindStrProvEntry{})))
		entry.Base = nil
	}
	m.setStrProvCount(n)
}

// AppendStrProv records one retired generation and reports whether the
// fixed-capacity history had room for it.
func (m *BindMachine) AppendStrProv(e BindStrProvEntry) bool {
	cnt := m.StrProvCount()
	if cnt >= BindStrProvMax {
		return false
	}
	entry := (*BindStrProvEntry)(unsafe.Add(unsafe.Pointer(m),
		BindMachineStrProvOffset+uintptr(cnt)*unsafe.Sizeof(e)))
	*entry = e
	m.setStrProvCount(cnt + 1)
	return true
}
