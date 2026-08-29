package bind

import (
	"unsafe"

	"github.com/velox-io/json/internal/valueabi"
	"github.com/velox-io/json/native/ndec"
)

// serveTapeBindValue handles a tape walk reaching a value.Value destination.
// This occurs during JSON variant rebinds and public UnmarshalValue calls.
//
// Doc.Tape aliases the source base through the subvalue end. Base remains zero,
// Tidx selects the subvalue, and End bounds it because container close indices
// are absolute from the source base. Mode preserves the active seam view. The
// Doc slices keep the aliased tape, string arena, and source reachable for the
// lifetime of the Value.
func (p *Parser) serveTapeBindValue(m *ndec.BindMachine) error {
	sourceBase := m.Alloc.ValueTape
	subStart := int(m.Yield.Arg0)
	subWords := int(m.Yield.Arg1)
	stash := (*tapeValueYieldStash)(unsafe.Pointer(&m.Core.Stash[0]))
	target := stash.slot
	viewMode := stash.viewMode

	// Alias the source tape from base to one past the sub-tree. Container end
	// indices are absolute from base, so the view must cover [0, subStart+subWords).
	end := subStart + subWords
	strArena := unsafe.Slice(m.Alloc.StrArena, m.Alloc.StrArenaCap)
	doc := &valueabi.Doc{
		Tape:     unsafe.Slice(sourceBase, end),
		StrArena: strArena[:m.Core.StrUsed],
		// Src preserves the source coordinate space for raw-string entries.
		Src: unsafe.Slice(m.Ctx.Src, m.Ctx.SrcLen),
	}

	// Store performs a typed descriptor assignment so the doc write barrier fires.
	valueabi.Store(target, valueabi.Descriptor{
		Doc:  doc,
		Tidx: int32(subStart),
		End:  int32(end),
		Mode: int32(viewMode),
	})

	// m.Alloc.ValueTape stays as the source base; C continues the walk.
	return nil
}

// tapeValueYieldStash mirrors the C stash union member tape_value_yield: the
// destination slot followed by the active view mode recorded before the yield.
type tapeValueYieldStash struct {
	slot     unsafe.Pointer
	viewMode uint32
	_        uint32
}
