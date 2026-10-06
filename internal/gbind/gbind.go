// Package gbind binds JSON into a vbind.TypeTree destination in pure Go. It
// is the bind engine wherever the native ndec blob is not linked, and it
// reproduces the native binder's policy at every position (root, struct
// field, array element, map value): which null clears, which mismatch is
// recorded and skipped, which aborts, and which offset an error names.
//
// The walk is recursive descent straight over the source bytes; its cursor
// stops at the token starts the native structural index holds. The strict
// body policy validates inside the walk, as each string span is passed, so
// one pass covers it, while a lax verdict can only fail when the walk stops
// short of the end, so it runs on that error path alone. Storage is plain
// Go
// allocation; the native allocator protocol has no counterpart here. What
// only the caller can provide, hook calls and stream scope routing, crosses
// Host. Engine errors surface as *Error in the native error vocabulary
// (ndec.BindErr*), so one translation serves both engines.
package gbind

import (
	"errors"
	"runtime"
	"strconv"
	"unsafe"

	"github.com/velox-io/json/gort"
	"github.com/velox-io/json/internal/gdec"
	"github.com/velox-io/json/native/ndec"
	"github.com/velox-io/json/stream"
	"github.com/velox-io/json/vbind"
)

// maxDepth mirrors BIND_MAX_DEPTH: the deepest container nesting a bind
// accepts.
const maxDepth = 255

// maxPtrChain mirrors the 32-layer limit on pointer chains below a field,
// element, or map value.
const maxPtrChain = 32

// NoPos marks an Error without a source position.
const NoPos = ^uint64(0)

// Error is an engine error payload. Pos is a document offset or NoPos.
// TypeIdx names the type the error reports against; Target is the variant
// host for discriminator errors.
type Error struct {
	Kind    uint32
	Detail  uint32
	Pos     uint64
	TypeIdx int
	Target  unsafe.Pointer
}

func (e *Error) Error() string { return "gbind: bind error kind " + strconv.Itoa(int(e.Kind)) }

// errAbort unwinds the walk once binder.info holds the error payload.
var errAbort = errors.New("gbind: abort")

// Host provides what the engine cannot own: running a staged hook against
// its target, and the stream scope stack shared with the caller's other
// engine. Errors Host returns pass through Bind unchanged.
type Host interface {
	// Deferred runs one staged hook. data is the trimmed raw JSON span for
	// Unmarshaler, RawMessage, and non-empty interface targets, and the
	// decoded string body for TextUnmarshaler and base64 []byte targets.
	// borrowed reports that data aliases caller-owned input under the
	// zero-copy opt. docOff is the span's document offset, zero when it
	// has none.
	Deferred(kind vbind.Kind, typeIdx uint16, target unsafe.Pointer, data []byte, borrowed bool, docOff int64) error

	PushStreamScope(addr unsafe.Pointer, elemHasStream bool) int
	PopStreamScope(idx int)
	PeekAnyScopeBreak() *stream.BreakSignal
	StashScopeBreak(sig *stream.BreakSignal) bool
}

// Plan is the engine's immutable view of a TypeTree: runtime types per type
// index, which structs bind as poly hosts, and which types hold pointers.
type Plan struct {
	tt       *vbind.TypeTree
	rtypes   []unsafe.Pointer // type idx: runtime *_type
	polyHost []bool           // struct type idx: binds through phase 2
	ptrs     []bool           // type idx: values hold pointers the GC scans
	keys     []*keyTable      // struct type idx: JSON key to field
	maps     []*mapPlan       // map type idx: how its entries stage
	nmaps    int              // map types that stage, the region free lists a binder keeps
	memoLen  int              // key memo bytes, the TypeTree's KeyMemoLen
}

// NewPlan builds the view of tt.
func NewPlan(tt *vbind.TypeTree) *Plan {
	pl := &Plan{
		tt:       tt,
		rtypes:   make([]unsafe.Pointer, len(tt.Types)),
		polyHost: make([]bool, len(tt.Types)),
		ptrs:     make([]bool, len(tt.Types)),
		keys:     make([]*keyTable, len(tt.Types)),
		maps:     make([]*mapPlan, len(tt.Types)),
		memoLen:  tt.KeyMemoLen,
	}
	for i := range tt.Types {
		if i < len(tt.ReflectTypes) && tt.ReflectTypes[i] != nil {
			pl.rtypes[i] = gort.TypePtr(tt.ReflectTypes[i])
			pl.ptrs[i] = hasPointers(tt.ReflectTypes[i])
		}
	}
	for i := range tt.Types {
		bt := &tt.Types[i]
		if bt.Kind == vbind.KindMap {
			pl.maps[i] = pl.newMapPlan(uint32(i))
		}
		if bt.Kind != vbind.KindStruct {
			continue
		}
		sm := tt.TypeMeta[i].StructMeta()
		pl.polyHost[i] = collectsEntries(sm)
		first := bt.StructFirstFieldIndex(&tt.Fields[0])
		kt := newKeyTable(sm.Lookup,
			tt.FieldNames[first:first+bt.Struct().FieldCount])
		pl.keys[i] = kt
		kt.memoRow = sm.KeyMemo
		kt.fields = make([]fieldPlan, bt.Struct().FieldCount)
		for j := range kt.fields {
			f := &tt.Fields[first+uint32(j)]
			if f.Flags&uint32(vbind.TagVariant|vbind.TagKindof) != 0 {
				pl.polyHost[i] = true
			}
			fti := f.FieldTypeIndex(&tt.Types[0])
			kt.fields[j] = fieldPlan{
				f: f, off: uintptr(f.Offset), ti: fti, kind: tt.Types[fti].Kind,
				plain: f.Flags&uint32(vbind.TagQuoted) == 0 && !vbind.FieldViaPtr(f),
			}
		}
	}
	return pl
}

// Input is one bind call.
type Input struct {
	Src []byte
	// Alias is the caller-owned buffer Src was taken from, which zero-copy
	// strings alias; nil means Src itself.
	Alias []byte
	// Base is the document offset of Src[0], which rebases error positions.
	Base uint64
	// Opt holds the ndec.BindOpt* bits of the call.
	Opt uint32
	// Tape selects UnmarshalValue semantics for text standing in for a
	// Value tape: errors carry no position, as the caller never saw the
	// text, and null leaves a Value destination invalid.
	Tape bool
	// State is the caller's reusable bind storage.
	State *State
	// Alloc is the allocator the native binder shares: every value the
	// bind produces is carved from its slot classes and string arena. The
	// caller owns its release points.
	Alloc *vbind.Allocator
}

// State is the storage a caller keeps across binds: the bind state and the
// buffers it grows. One Bind uses a State at a time.
type State struct {
	c       binder
	scratch []byte // unescape buffer
}

// Footprint reports the bytes the State retains between binds.
func (s *State) Footprint() int {
	c := &s.c
	n := cap(s.scratch) + cap(c.deferred)*int(unsafe.Sizeof(deferred{})) +
		cap(c.docs)*int(unsafe.Sizeof(stagedDoc{})) + cap(c.pending)*int(unsafe.Sizeof(mapAssign{}))
	if pl := c.regionPlan; pl != nil {
		for _, mp := range pl.maps {
			if mp != nil && mp.ert != nil {
				n += len(c.regions[mp.list]) * regionSlots * int(mp.stride)
			}
		}
		n += len(c.memo)
	}
	return n
}

// Bind binds in.Src into dst. settled reports that the walk consumed the
// whole value, so an error it returns left the input positioned after it.
// A strict failure may leave dst partially written, as the policy verdict
// arrives while the walk binds.
func Bind(pl *Plan, h Host, in *Input, dst unsafe.Pointer) (settled bool, err error) {
	strict := in.Opt&ndec.BindOptStrictScan != 0
	st := in.State
	c := &st.c
	c.pl, c.tt, c.host, c.src, c.opt, c.base, c.tape = pl, pl.tt, h, in.Src, in.Opt, in.Base, in.Tape
	c.strict = strict
	c.txt = mkText(in.Src)
	if c.regionPlan != pl {
		c.regions, c.memo, c.regionPlan = make([][]unsafe.Pointer, pl.nmaps), make([]byte, pl.memoLen), pl
	}
	c.scratch = &st.scratch
	c.a = in.Alloc
	if c.tt.HasStreamField {
		// Stream scopes carve from their own views, so the root reserves
		// the input bound alone.
		c.a.EnsureStrArenaExact(len(in.Src))
	} else {
		c.a.EnsureStrArena(len(in.Src))
	}
	c.strs = c.a.StrArena
	defer c.release()
	if c.opt&ndec.BindOptZeroCopyStr != 0 {
		c.strBase = in.Src
		if in.Alias != nil {
			c.strBase = in.Alias
		}
		c.zc = unsafe.Pointer(unsafe.SliceData(c.strBase))
	}
	c.to(0)
	err = c.root(dst)
	runtime.KeepAlive(dst)
	// A walk that reached the end tokenized every byte as the scan does,
	// which proves the lax verdict.
	whole := err == nil && c.eof()
	if err == nil {
		settled = true
		err = c.finish()
	}
	// A walk error the scan verdict also reaches reports as the scan's,
	// which precedes it in the native machine. The scan runs on this error
	// path alone.
	if err != nil && !whole && !gdec.Check(in.Src, strictMode(strict)) {
		return false, errScan()
	}
	if err == errAbort {
		e := c.info
		switch {
		case c.tape:
			e.Pos = NoPos
		case e.Pos != NoPos:
			e.Pos += c.base
		}
		return settled, &e
	}
	return settled, err
}

// strictMode maps the strict scan bit to the scan verdict it names.
func strictMode(strict bool) gdec.ScanMode {
	if strict {
		return gdec.ScanStrict
	}
	return gdec.ScanLax
}

// release commits the string arena bytes the bind wrote, which its values
// may hold even on failure, drops every reference the bind took, and keeps
// the staging backings, cleared, for the next bind.
func (c *binder) release() {
	c.a.CommitStrArena(c.strUsed)
	d, s, p := c.deferred, c.docs, c.pending
	clear(d)
	clear(s)
	clear(p)
	*c = binder{deferred: d[:0], docs: s[:0], pending: p[:0], regions: c.regions, memo: c.memo, regionPlan: c.regionPlan}
}

// errScan is the scan verdict's failure, which names no position.
func errScan() error { return &Error{Kind: ndec.BindErrSyntax, Pos: NoPos} }

// binder is the state of one Bind call.
type binder struct {
	src  []byte
	txt  text // src as the spine passes it
	p    int  // cursor of the paths off the spine: a token start in src
	pl   *Plan
	tt   *vbind.TypeTree
	host Host

	opt   uint32
	depth int
	base  uint64
	tape  bool

	// strict holds the strict scan bit of opt, read per string span the
	// walk passes.
	strict bool

	// strBase is what zero-copy strings alias: the caller-owned original,
	// byte-identical to src over [0, len(src)). Nil without the zero-copy opt.
	strBase []byte
	zc      unsafe.Pointer // strBase's data, nil without the zero-copy opt
	scratch *[]byte

	// a supplies all storage; strs is its string arena view, of which the
	// bind wrote strUsed bytes.
	a       *vbind.Allocator
	strs    []byte
	strUsed int
	streams int // open stream scopes with their own string views

	// The generic map assign takes its key by address; these hold it so
	// it need not escape per entry.
	intKey [8]byte
	strKey string

	// The first recorded type mismatch surfaces at document end against
	// mismatchType, the leaf destination that rejected the value.
	mismatch     bool
	mismatchPos  uint64
	mismatchType uint32
	rootType     uint32

	info Error
	// caseLift marks a mismatch abort already lifted to its innermost case
	// type, so an enclosing case descent does not lift it further.
	caseLift bool

	deferred []deferred
	docs     []stagedDoc
	pending  []mapAssign
	// regions holds, per staging map type of regionPlan, the zeroed
	// regions no open map holds.
	regions [][]unsafe.Pointer
	// memo is the key transition memo, one byte per struct field and its
	// sentinel, laid out as the TypeTree's KeyMemo rows: the same layout
	// the native Parser's memo holds. Entries survive binds: a stale byte
	// only mispredicts.
	memo []byte
	// regionPlan is the Plan regions and memo were built for; a Bind
	// through another Plan rebuilds both.
	regionPlan *Plan
	hookErr    error // first hook failure
	keyErr     error // first map-key conversion failure
}

// finish reports the walk's own errors, the recorded mismatch and trailing
// data, ahead of the final report point.
func (c *binder) finish() error {
	if c.mismatch {
		return c.failType(ndec.BindErrTypeMismatch, c.mismatchPos, c.mismatchType)
	}
	if !c.eof() {
		return c.fail(ndec.BindErrTrailing, c.pos())
	}
	return c.report()
}

func collectsEntries(sm *vbind.StructMetaPayload) bool {
	return sm.InlineVariantIdx != 0xFFFF || sm.ReserveUnknownFieldOff != 0xFFFFFFFF
}
