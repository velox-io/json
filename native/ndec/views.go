package ndec

import "unsafe"

// Machine-held views into Go-owned backings.
//
// The machine block is pooled noscan memory, but typed pointer stores into it
// still run write barriers that record the old slot value. A view left naming
// a backing whose Go root is gone turns the next store over that slot into a
// bad-pointer report once a GC cycle frees the backing. Every site that drops
// or replaces a backing the machine can see must clear its view first, while
// the Go root still retains the old backing; the site that regains a view
// installs it explicitly. TruncateStrProv follows the same rule for the
// provenance table.

// CursorPair returns the machine's structural cursor pair, two slots owned by
// C immediately after the native frame array. Go writes both when seeding a
// tape-bind root or installing a stream window.
func (m *BindMachine) CursorPair() *[2]unsafe.Pointer {
	return (*[2]unsafe.Pointer)(unsafe.Add(unsafe.Pointer(m), BindMachineCursorOffset))
}

// RawArena returns the machine's raw-scratch backing slot. The driver's raw
// slice is the lifetime root for the backing it names.
func (m *BindMachine) RawArena() *unsafe.Pointer {
	return (*unsafe.Pointer)(unsafe.Add(unsafe.Pointer(m), BindMachineRawArenaOffset))
}

// DropStructuralViews clears the structural ABI view and the cursor pair
// before the caller replaces the structural-index backing.
func (m *BindMachine) DropStructuralViews() {
	m.Alloc.Structural = nil
	c := m.CursorPair()
	c[0] = nil
	c[1] = nil
}

// DropRawView clears the raw-scratch view before the caller replaces or frees
// the raw backing.
func (m *BindMachine) DropRawView() {
	*m.RawArena() = nil
}

// DropWindowView clears the source view before the driver replaces or frees
// the window backing.
func (m *BindMachine) DropWindowView() {
	m.Ctx.Src = nil
}
