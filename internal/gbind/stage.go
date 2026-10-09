package gbind

import (
	"unsafe"

	"github.com/velox-io/json/internal/gdec"
	"github.com/velox-io/json/internal/valueabi"
	"github.com/velox-io/json/native/ndec"
	"github.com/velox-io/json/vbind"
)

// Staging. Writes that must not land mid-walk wait here:
//   - Hook calls run when the staging buffer fills, at a stream handoff, and
//     at document end. A slice growth moves the targets inside the old
//     backing instead of draining.
//   - Values are installed inline as they bind, but their documents publish
//     only from a report point that found no failure, as native publishes
//     its Doc: a failed parse leaves invalid Values, and copies made by a
//     growth or a map publication become valid with the original.
//   - Map entries whose values hold deferred targets publish after those
//     hooks wrote them.
//
// Hook and map-key failures never abort the walk. Drains record the first
// of each, walk errors take precedence, and the report points (document
// end, stream handoff) surface a hook failure before a key failure, so the
// verdict never depends on when the drains ran.

// maxStaged bounds the staged hooks, and with them the rebase scan of each
// slice growth.
const maxStaged = 16

type deferred struct {
	kind     vbind.Kind
	typeIdx  uint16
	target   unsafe.Pointer
	data     []byte
	borrowed bool
	docOff   int64 // document offset of the span, zero when it has none
}

// stagedDoc is an unpublished Value document and the tape it will expose.
type stagedDoc struct {
	doc  *valueabi.Doc
	tape []uint64
}

type mapAssign struct {
	info *vbind.MapDrainInfo
	m    unsafe.Pointer
	key  string
	val  unsafe.Pointer
	vrt  unsafe.Pointer
}

// deferValue stages a hook call, mirroring deferred_value. ctr is the type
// a string-kind mismatch reports against.
func (c *binder) deferValue(dst unsafe.Pointer, ti, ctr uint32) error {
	if len(c.deferred) == maxStaged {
		c.drainDeferred()
	}
	kind := c.typ(ti).Kind
	ch := c.peek()
	if kind == vbind.KindTextUnmarshaler && ch == 'n' {
		return c.atom()
	}
	start := c.p
	d := deferred{kind: kind, typeIdx: uint16(ti), target: dst}
	if kind == vbind.KindTextUnmarshaler || kind == vbind.KindSlice {
		if ch != '"' {
			return c.failType(ndec.BindErrTypeMismatch, uint64(start), ctr)
		}
		b, borrowed, err := c.str(true)
		if err != nil {
			return err
		}
		d.data = b
		if borrowed {
			d.docOff = int64(c.base) + int64(start+1)
		}
	} else {
		// The span reaches its hook as a complete value: the skip validates
		// it unless the lenient opt releases it.
		if err := c.skipValue(); err != nil {
			return err
		}
		span := c.src
		if c.strBase != nil {
			span, d.borrowed = c.strBase, true
		}
		d.data = gdec.TrimTrailingWS(span[start:c.p])
		d.docOff = int64(c.base) + int64(start)
	}
	c.deferred = append(c.deferred, d)
	return nil
}

// drainDeferred runs every staged hook, recording the first failure.
func (c *binder) drainDeferred() {
	for i := range c.deferred {
		d := &c.deferred[i]
		if err := c.host.Deferred(d.kind, d.typeIdx, d.target, d.data, d.borrowed, d.docOff); err != nil && c.hookErr == nil {
			c.hookErr = err
		}
	}
	clear(c.deferred)
	c.deferred = c.deferred[:0]
}

// rebase follows a slice growth: the n bytes at old now live at moved, so
// the hook targets among them move too.
func (c *binder) rebase(old unsafe.Pointer, n uintptr, moved unsafe.Pointer) {
	for i := range c.deferred {
		if off := uintptr(c.deferred[i].target) - uintptr(old); off < n {
			c.deferred[i].target = unsafe.Add(moved, off)
		}
	}
}

// stageDoc withholds the tape of a freshly installed Value's document.
func (c *binder) stageDoc(doc *valueabi.Doc) {
	c.docs = append(c.docs, stagedDoc{doc: doc, tape: doc.Tape})
	doc.Tape = nil
}

// publishDocs exposes the staged documents, making every Value over them,
// copies included, valid.
func (c *binder) publishDocs() {
	for i := range c.docs {
		c.docs[i].doc.Tape = c.docs[i].tape
	}
	clear(c.docs)
	c.docs = c.docs[:0]
}

// settle runs staged hooks and publishes the map entries they feed.
func (c *binder) settle() {
	c.drainDeferred()
	for i := range c.pending {
		pa := &c.pending[i]
		c.publishMapEntry(pa.info, pa.m, pa.key, pa.val, pa.vrt)
	}
	clear(c.pending)
	c.pending = c.pending[:0]
}

// report is a report point: it settles, returns the first recorded failure,
// and otherwise publishes the staged documents so a reader sees complete
// values.
func (c *binder) report() error {
	c.settle()
	if c.hookErr != nil {
		return c.hookErr
	}
	if c.keyErr != nil {
		return c.keyErr
	}
	c.publishDocs()
	return nil
}
