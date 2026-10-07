package gbind

import (
	"errors"
	"iter"
	"reflect"
	"unsafe"

	"github.com/velox-io/json/gort"
	"github.com/velox-io/json/stream"
)

// A stream handler pulls its array: every Iter step asks the driver for the
// next batch or element. The array loop runs as a coroutine the driver
// resumes to the next stop point, so the recursive walk needs no resumable
// state of its own. Nested streams nest coroutines: an element body bound
// inside the outer loop activates the inner stream there.

// activator is the read side every Stream[T] implements.
type activator interface {
	ActivateRead(stream.ScopeDriver, unsafe.Pointer, int, stream.StopReason) error
}

type streamDriver struct {
	c             *binder
	hdr           *gort.SliceHeader
	elemTi        uint32
	arrTi         uint32
	elemRT        unsafe.Pointer
	esz           uintptr
	batch         int
	elemHasStream bool
	retainMark    int // allocator retention height at activation

	next   func() (stream.StopReason, bool)
	skip   bool // drain: skip the remaining elements without binding
	closed bool // the loop reached this array's ']'
	err    error
}

var _ stream.ScopeDriver = (*streamDriver)(nil)

// loop is the array walk, entered after '['. It yields at each stop point:
// a full leaf batch, a non-leaf element slot before its body binds, and the
// close.
func (d *streamDriver) loop(yield func(stream.StopReason) bool) {
	c := d.c
	if c.accept(']') {
		d.hdr.Len = 0
		d.closed = true
		yield(stream.StopClosed)
		return
	}
	if d.err = c.push(); d.err != nil {
		return
	}
	for {
		if !d.skip {
			switch {
			case d.elemHasStream:
				d.hdr.Len = 0
				if !yield(stream.StopElement) {
					return
				}
			case d.hdr.Len == d.batch:
				if !yield(stream.StopBatch) {
					return
				}
			}
		}
		if d.skip {
			d.err = c.skipValue()
		} else {
			slot := unsafe.Add(d.hdr.Data, uintptr(d.hdr.Len)*d.esz)
			d.hdr.Len++
			c.p, d.err = c.bindValue(c.txt, c.p, slot, d.elemTi, d.arrTi, siteElem, false)
		}
		if d.err != nil {
			return
		}
		switch c.peek() {
		case ',':
			c.next()
		case ']':
			c.next()
			c.pop()
			d.hdr.Cap = d.hdr.Len
			d.closed = true
			yield(stream.StopClosed)
			return
		default:
			d.err = c.closeErr()
			return
		}
	}
}

func (d *streamDriver) DriveBind() (stream.StopReason, error) {
	r, ok := d.next()
	if d.err != nil {
		return stream.StopNone, d.err
	}
	if !ok {
		r = stream.StopClosed
	}
	if d.c.host.PeekAnyScopeBreak() != nil {
		return stream.StopBreak, nil
	}
	return r, nil
}

func (d *streamDriver) CurrentBatch() (unsafe.Pointer, int) { return d.hdr.Data, d.hdr.Len }

func (d *streamDriver) ElemHasStream() bool { return d.elemHasStream }

func (d *streamDriver) PeekAnyBreak() *stream.BreakSignal { return d.c.host.PeekAnyScopeBreak() }

func (d *streamDriver) GrowBatch(reuse bool) error {
	if reuse {
		// The reused backing starts zero, like a fresh one.
		gort.MemclrHasPointers(d.hdr.Data, uintptr(d.batch)*d.esz)
	} else {
		d.hdr.Data = gort.UnsafeNewArray(d.elemRT, d.batch)
		d.hdr.Cap = d.batch
	}
	d.hdr.Len = 0
	return nil
}

func (d *streamDriver) DrainRemaining() error {
	d.skip = true
	return nil
}

// SettleBatch is the handoff report point. The backings the batch's values
// took leave the allocator's retention, so a long stream holds only what
// its handler keeps.
func (d *streamDriver) SettleBatch() error {
	if err := d.c.report(); err != nil {
		return err
	}
	d.c.a.ReleaseScoped(d.retainMark)
	return nil
}

// bindStream activates a Stream[T] at dst for the array after its '['. It
// mirrors the native activation: the first batch (or first element slot)
// is reached before the handler runs, an early handler exit drains the
// rest, and a BreakSignal for an outer scope is stashed for that scope.
func (c *binder) bindStream(dst unsafe.Pointer, ti uint32) error {
	h := c.host
	bt := c.typ(ti)
	sm := c.tt.TypeMeta[ti].SliceMeta()
	d := &streamDriver{
		c:             c,
		hdr:           (*gort.SliceHeader)(dst),
		elemTi:        c.child(ti),
		arrTi:         ti,
		elemRT:        sm.ElemRType,
		esz:           uintptr(bt.Slice().ChildSize),
		batch:         int(c.tt.Slots[sm.AllocClass].Batch),
		elemHasStream: bt.HasElemHasStream(),
		retainMark:    c.a.RetainMark(),
	}
	defer c.a.ReleaseScoped(d.retainMark)
	if c.tt.TypeWritesStr[ti] {
		parent, used := c.strs, c.strUsed
		c.streams++
		c.newStrView(scopeStrView)
		defer func() {
			c.streams--
			c.a.StrArena, c.strs, c.strUsed = parent, parent, used
		}()
	}
	next, stop := iter.Pull(iter.Seq[stream.StopReason](d.loop))
	defer stop()
	d.next = next
	defer func() { d.hdr.Data = nil }()

	idx := h.PushStreamScope(dst, d.elemHasStream)
	defer h.PopStreamScope(idx)

	if h.PeekAnyScopeBreak() != nil {
		return d.drain()
	}
	d.hdr.Data = gort.UnsafeNewArray(d.elemRT, d.batch)
	d.hdr.Cap, d.hdr.Len = d.batch, 0
	reason, err := d.DriveBind()
	if err != nil {
		return err
	}
	if reason != stream.StopElement {
		if err = c.report(); err != nil {
			return err
		}
	}
	data, n := d.CurrentBatch()
	if reason == stream.StopElement {
		n = 1
	}
	act := reflect.NewAt(c.tt.ReflectTypes[ti], dst).Interface().(activator)
	err = act.ActivateRead(d, data, n, reason)
	var sig *stream.BreakSignal
	if errors.As(err, &sig) {
		if !h.StashScopeBreak(sig) {
			return sig
		}
	} else if err != nil {
		return err
	}
	return d.drain()
}

// drain skips the elements the handler left and runs the loop to its close.
func (d *streamDriver) drain() error {
	d.skip = true
	for !d.closed {
		if _, ok := d.next(); !ok {
			break
		}
	}
	if d.err != nil {
		return d.err
	}
	d.c.settle()
	return nil
}
