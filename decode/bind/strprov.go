package bind

import (
	"errors"
	"unsafe"

	"github.com/velox-io/json/native/ndec"
)

// String-arena provenance for streaming parses.
//
// A growth of the string arena displaces the backing while published string
// headers keep pointing into it, so the machine carries a table of retired
// generations (ndec.BindStrProvEntry) that poly case selection consults to
// prove a discriminator was published by this parse. Native reads the table
// and resets its count at the streaming root bootstrap; every other write
// is driver-side, here.
//
// Ownership follows the arena views as a stack discipline. Entries recorded
// while a stream scope's views are installed belong to that scope: their
// intervals name the scope's own generations, consultable only by elements
// bound inside the current one. installScopeViews snapshots the table height
// as the scope's floor; each batch boundary and the scope exit truncate back
// to it, so a long stream stays within the table's fixed capacity. Entries
// below the floor belong to outer scopes and survive until their own
// boundaries or parse end.
//
// Every truncation nils the dropped entries while their retired backings are
// still retained, ahead of the release that drops those backings. The table
// lives in the pooled noscan machine block; a truncation that left a base
// behind would let the next append's write barrier record the dead pointer as
// its old value, tripping the runtime's bad-pointer check once a GC cycle has
// freed the backing.

// recordStrProv keeps one retired string-arena generation addressable. The
// allocator's retained set keeps the backing alive.
func recordStrProv(m *ndec.BindMachine, displaced unsafe.Pointer, used int) error {
	if !m.AppendStrProv(ndec.BindStrProvEntry{
		Base:  displaced,
		Start: uint32(m.Alloc.StrGenStart),
		End:   uint32(used),
	}) {
		return errors.New("vjson: string arena growth history exhausted")
	}
	return nil
}
