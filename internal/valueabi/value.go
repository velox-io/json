// Package valueabi defines the shared representation of tape-backed JSON values.
package valueabi

import "unsafe"

const (
	TagArrBeg  = '['
	TagArrEnd  = ']'
	TagObjBeg  = '{'
	TagObjEnd  = '}'
	TagString  = '"'
	TagStrRaw  = 'R'
	TagStrFree = 'S'
	TagInt64   = 'l'
	TagUint64  = 'u'
	TagDouble  = 'd'
	TagNumRaw  = 'D'
	TagTrue    = 't'
	TagFalse   = 'f'
	TagNull    = 'n'
)

const (
	SeamBit   = uint64(1) << 63
	SeamBits  = 31
	SeamMask  = 0x7FFFFFFF
	SeamViewA = 0
	SeamViewB = SeamBits

	PayloadMask = 0x00FFFFFFFFFFFFFF
)

// Descriptor mode bits. The low ViewShiftMask bits select the seam view; bits
// above are independent flags, so every seam consumer must mask before
// shifting. The packing is an ABI agreement with TAPE_VIEW_SHIFT_MASK and
// TAPE_MODE_COUNT_AT_CLOSE in ndec's core/tape.h and VJ_TVIEW_SHIFT_MASK in
// encvm's tapewalk.h.
const (
	ViewShiftMask = 0x1F

	// ModeCountAtClose stores the root's member count in the matching close
	// word's high24 rather than the begin word's.
	ModeCountAtClose = 1 << 8

	ModeInlineDualRoot  = SeamViewA | ModeCountAtClose
	ModeReserveDualRoot = SeamViewB
)

// Doc owns the buffers addressed by a tape. Tape becoming non-empty publishes
// the document. StrArena length is its published extent while capacity preserves
// the readable padding and producer-owned append space.
type Doc struct {
	Tape     []uint64
	StrArena []byte
	Src      []byte
	ZeroCopy bool
}

// Descriptor identifies one logical value in a Doc. Base is the origin for
// paired container indices, Tidx is the navigation cursor, End bounds the
// reachable region, and Mode packs the seam view with descriptor flags.
type Descriptor struct {
	Doc  *Doc
	Base int32
	Tidx int32
	End  int32
	Mode int32
}

// Load copies a Descriptor from its ABI address.
func Load(ptr unsafe.Pointer) Descriptor {
	return *(*Descriptor)(ptr)
}

// Store writes a Descriptor through its ABI address.
func Store(ptr unsafe.Pointer, desc Descriptor) {
	*(*Descriptor)(ptr) = desc
}

// HasTape reports whether the descriptor addresses a published tape.
func (d *Descriptor) HasTape() bool {
	return d.Doc != nil && len(d.Doc.Tape) > 0
}

// WordAt loads the tape word at a base-relative index.
func (d *Descriptor) WordAt(idx int) uint64 {
	return *(*uint64)(d.wordPtr(idx))
}

//go:nosplit
func (d *Descriptor) wordPtr(idx int) unsafe.Pointer {
	return unsafe.Add(unsafe.Pointer(unsafe.SliceData(d.Doc.Tape)), uintptr(int(d.Base)+idx)*8)
}

// TagAt returns the tape tag at a base-relative index.
func (d *Descriptor) TagAt(idx int) byte {
	return byte(d.WordAt(idx) >> 56)
}

// PayloadAt returns the tape payload at a base-relative index.
func (d *Descriptor) PayloadAt(idx int) uint64 {
	return d.WordAt(idx) & PayloadMask
}

// IsSeam reports whether a tape word carries seam distances.
func IsSeam(word uint64) bool {
	return int64(word) < 0
}

// IsStringTag reports whether tag denotes a string word.
func IsStringTag(tag byte) bool {
	return tag == TagString || tag == TagStrRaw || tag == TagStrFree
}

// ContainerCount returns the member count of the container at idx. A root
// published with ModeCountAtClose stores its count in the matching close
// word's high24; every other container stores it in its begin word. The idx
// check confines the close-word rule to the shared dual root: children
// inherit the mode flag while keeping begin-word counts.
func (d *Descriptor) ContainerCount(idx int) int {
	if d.Mode&ModeCountAtClose != 0 && idx == 0 {
		close := d.ContainerEnd(idx)
		return int((d.WordAt(close) >> 32) & 0xFFFFFF)
	}
	return int((d.WordAt(idx) >> 32) & 0xFFFFFF)
}

// ContainerEnd returns the base-relative index of a container close word.
func (d *Descriptor) ContainerEnd(idx int) int {
	return int(d.WordAt(idx) & 0xFFFFFFFF)
}

// SkipSeams advances to the next value in the selected seam view.
func (d *Descriptor) SkipSeams(idx int) int {
	shift := uint(d.Mode & ViewShiftMask)
	for IsSeam(d.WordAt(idx)) {
		distance := int((d.WordAt(idx) >> shift) & SeamMask)
		if distance == 0 {
			distance = 1
		}
		idx += distance
	}
	return idx
}

// ValueEnd returns the index following the value at idx.
func (d *Descriptor) ValueEnd(idx int) int {
	switch d.TagAt(idx) {
	case TagArrBeg, TagObjBeg:
		return d.ContainerEnd(idx) + 1
	case TagInt64, TagUint64, TagDouble:
		return idx + 2
	default:
		return idx + 1
	}
}

// Skip returns the next value index in the selected seam view.
func (d *Descriptor) Skip(idx int) int {
	idx = d.SkipSeams(idx)
	return d.SkipSeams(d.ValueEnd(idx))
}

// Extent returns the root index and its exclusive end.
func (d *Descriptor) Extent() (root, end int) {
	if !d.HasTape() {
		return 0, 0
	}
	root = d.SkipSeams(int(d.Tidx))
	return root, d.ValueEnd(root)
}

// StringAt returns a StrArena span encoded at idx.
func (d *Descriptor) StringAt(idx int) []byte {
	off, length := d.span(idx)
	return arenaRange(d.Doc.StrArena, off, length)
}

// NumRawAt returns source digits stored in StrArena.
func (d *Descriptor) NumRawAt(idx int) []byte {
	return d.StringAt(idx)
}

// RawStringAt returns a Src span encoded at idx.
func (d *Descriptor) RawStringAt(idx int) []byte {
	off, length := d.span(idx)
	return arenaRange(d.Doc.Src, off, length)
}

// ScalarStringAt resolves every string tag to its backing bytes.
func (d *Descriptor) ScalarStringAt(idx int) []byte {
	if d.TagAt(idx) == TagStrRaw {
		return d.RawStringAt(idx)
	}
	return d.StringAt(idx)
}

func (d *Descriptor) span(idx int) (off, length uint32) {
	payload := d.PayloadAt(idx)
	return uint32(payload), uint32((payload >> 32) & 0xFFFFFF)
}

func arenaRange(arena []byte, off, length uint32) []byte {
	return unsafe.Slice((*byte)(unsafe.Add(unsafe.Pointer(unsafe.SliceData(arena)), uintptr(off))), int(length))
}

// Int64At returns the signed value following the tag at idx.
func (d *Descriptor) Int64At(idx int) int64 {
	return int64(*(*uint64)(d.wordPtr(idx + 1)))
}

// Uint64At returns the unsigned value following the tag at idx.
func (d *Descriptor) Uint64At(idx int) uint64 {
	return *(*uint64)(d.wordPtr(idx + 1))
}

// DoubleAt returns the float value following the tag at idx.
func (d *Descriptor) DoubleAt(idx int) float64 {
	return *(*float64)(d.wordPtr(idx + 1))
}
