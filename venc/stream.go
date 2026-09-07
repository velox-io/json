package venc

import (
	"errors"
	"reflect"
	"runtime"
	"unsafe"

	"github.com/velox-io/json/stream"
)

// errStreamOmitted is the internal sentinel for a member stream that produced
// nothing and is omitted (no OnWrite producer, or an empty producer, under
// omitempty). The fallback callers advance past the field without touching
// the parent's first latch.
var errStreamOmitted = errors.New("venc: stream field omitted")

// streamConfigError reports a Stream value with no registered OnWrite
// producer at a position where omission is not allowed.
type streamConfigError struct {
	typ reflect.Type
}

func (e *streamConfigError) Error() string {
	return "vjson: stream field has no OnWrite producer: " + e.typ.String()
}

// writeActivator mirrors the read-side streamActivator seam: every
// Stream[T] instantiation satisfies it through ActivateWrite, so the encoder
// invokes the producer without knowing T. One reflect conversion per
// activation; elements cross the seam as plain addresses.
type writeActivator interface {
	ActivateWrite(stream.WriteDriver) (bool, error)
}

// streamElemDrainMark is the low-water mark for element-boundary drains in
// stream mode: once the committed output reaches it, the driver flushes the
// writer before accepting the next element. The interpreter appends without a
// window, so without this drain a long producer would accumulate the whole
// output in memory between native BUF_FULL cycles.
const streamElemDrainMark = streamBufInitSize / 4

// Stream member-prefix protocols. The native VM pre-writes a container's
// first member line (newline + indent) before its field ops, so a first
// fallback member writes its key only; the interpreter has each member write
// its own leading newline; the Encode entry is called with the prefix
// already written (map values, interface payloads, pointer pointees).
const (
	streamPrefixNative uint8 = iota
	streamPrefixInterp
	streamPrefixNone
)

// streamDriver owns one stream activation: the JSON array framing around the
// elements a producer submits and the element-boundary drain. The member
// prefix commits lazily: whether an omitempty stream field appears at all is
// only known after the producer has run, and the producer must run exactly
// once.
type streamDriver struct {
	es     *encodeState
	elemTI *EncTypeInfo

	prefix      uint8 // streamPrefixNative / Interp / None
	hasKey      bool
	key         []byte
	omit        bool
	parentFirst bool
	// elemNL refines the keyless interpreter prefix: true in element
	// position (the stream writes its own leading newline), false after a
	// written key (pointer pointee: the array continues the line).
	elemNL bool

	opened      bool // '[' written
	emitted     int  // elements successfully encoded
	savedIndent int  // es.indentDepth at activation
}

// EncodeValue encodes one element at p and owns the separator before it. The
// first element opens the member prefix and the array; later ones join with a
// comma. The sink calls it only while the activation is error-free, so a
// failed call is always the last.
func (d *streamDriver) EncodeValue(p unsafe.Pointer) error {
	es := d.es
	native := es.useNativeVM
	if !d.opened {
		d.opened = true
		d.writeMemberPrefix()
		es.buf = append(es.buf, '[')
		if es.indentString != "" {
			es.indentDepth++
			if native {
				// The native engine's containers pre-write the first
				// element's line; its element ops then write nothing before
				// a first value.
				es.appendNewlineIndent()
			}
		}
	} else {
		es.buf = append(es.buf, ',')
		if native && es.indentString != "" {
			es.appendNewlineIndent()
		}
	}
	// Interpreter elements run their Blueprint (which writes its own leading
	// newline per op, seeded through execStreamElement), so the driver only
	// supplies the comma; native elements write nothing before a first
	// value, so the driver pre-writes the first element's line above.
	interpElem := !native && es.indentString != ""
	if err := es.execStreamElement(d.elemTI, p, interpElem); err != nil {
		return err
	}
	runtime.KeepAlive(p)
	d.emitted++
	return d.drain()
}

// writeMemberPrefix commits the parent's comma and key. It runs only when the
// first element proves the field is not omitted. The newline protocol follows
// the parent's engine: the native VM pre-wrote a first member's line, the
// interpreter's members write their own.
func (d *streamDriver) writeMemberPrefix() {
	if d.prefix == streamPrefixNone {
		return
	}
	es := d.es
	indent := es.indentString != ""
	if d.hasKey {
		if !d.parentFirst {
			es.buf = append(es.buf, ',')
			if indent {
				es.appendNewlineIndent()
			}
		} else if indent && d.prefix == streamPrefixInterp {
			// The interpreter has no pre-write: a first keyed member writes
			// its own line. The native VM already wrote it.
			es.appendNewlineIndent()
		}
		es.buf = append(es.buf, d.key...)
		if indent {
			es.buf = append(es.buf, ' ')
		}
		return
	}
	// Keyless: element position or pointer pointee.
	if !d.parentFirst {
		es.buf = append(es.buf, ',')
		if indent {
			es.appendNewlineIndent()
		}
		return
	}
	if indent && d.elemNL {
		es.appendNewlineIndent()
	}
}

// drain flushes the output window to the writer once the committed bytes
// reach the drain mark. flush retains short-write tails, so the loop retries
// until the window is empty; a zero-progress write surfaces as
// io.ErrShortWrite from streamState.flush.
func (d *streamDriver) drain() error {
	es := d.es
	if es.mode != modeStream || len(es.buf) < streamElemDrainMark {
		return nil
	}
	for len(es.buf) > 0 {
		if err := es.stream.flush(es); err != nil {
			return err
		}
	}
	return nil
}

// finish completes the framing after the producer returned. An empty producer
// writes the bare member prefix plus [] (or nothing at all under omitempty);
// a producing one closes the array at the parent's indent depth.
func (d *streamDriver) finish() error {
	es := d.es
	if d.emitted > 0 {
		if es.indentString != "" {
			es.indentDepth--
			es.appendNewlineIndent()
		}
		es.buf = append(es.buf, ']')
		return nil
	}
	if d.prefix == streamPrefixNone {
		es.buf = append(es.buf, '[', ']')
		return nil
	}
	if d.omit {
		return errStreamOmitted
	}
	d.writeMemberPrefix()
	es.buf = append(es.buf, '[', ']')
	return nil
}

// encodeStreamField activates the OnWrite producer on the Stream value at ptr
// and completes the array framing around whatever it submits. It returns
// errStreamOmitted for an omitted member (no producer, or an empty producer,
// under omitempty).
func (es *encodeState) encodeStreamField(
	sti *EncTypeInfo, ptr unsafe.Pointer,
	prefix uint8, hasKey bool, key []byte, omit bool, parentFirst bool, elemNL bool,
) error {
	d := &streamDriver{
		es:          es,
		elemTI:      sti.ResolveStream().ElemType,
		prefix:      prefix,
		hasKey:      hasKey,
		key:         key,
		omit:        omit,
		parentFirst: parentFirst,
		elemNL:      elemNL,
		savedIndent: es.indentDepth,
	}
	defer func() { es.indentDepth = d.savedIndent }()

	act := reflect.NewAt(sti.Type, ptr).Interface().(writeActivator)
	configured, err := act.ActivateWrite(d)
	if err != nil {
		return err
	}
	if !configured {
		if prefix != streamPrefixNone && omit {
			return errStreamOmitted
		}
		return &streamConfigError{typ: sti.Type}
	}
	return d.finish()
}

// streamFromYield serves a native fallback yield on a stream field: the VM
// suspends before any prefix write, the driver runs the producer with the
// lazy-commit framing, and the parent's PC and first latch advance according
// to what was actually written.
func (es *encodeState) streamFromYield(ctx *VjExecCtx, fb *fbInfo, fieldPtr unsafe.Pointer, isFirst bool) error {
	err := es.encodeStreamField(
		fb.TI, fieldPtr,
		streamPrefixNative, len(fb.KeyBytes) > 0, fb.KeyBytes,
		fb.TagFlags&EncTagFlagOmitEmpty != 0, isFirst, false,
	)
	if err == errStreamOmitted {
		ctx.PC += 8
		return nil
	}
	if err != nil {
		return err
	}
	ctx.PC += 8
	ctx.VMState &^= vjStFirstBit
	return nil
}

// streamFromInterp is the interpreter's opFallback counterpart of
// streamFromYield. The interpreter has no pre-write: a keyed stream field
// writes its own leading newline, a keyless one in element position writes
// the newline the elemNL latch carries, and a keyless value after a written
// key (pointer pointee) continues the line.
func (es *encodeState) streamFromInterp(fb *fbInfo, fieldPtr unsafe.Pointer, isFirst, elemNL bool) error {
	hasKey := len(fb.KeyBytes) > 0
	return es.encodeStreamField(
		fb.TI, fieldPtr,
		streamPrefixInterp, hasKey, fb.KeyBytes,
		fb.TagFlags&EncTagFlagOmitEmpty != 0, isFirst, elemNL,
	)
}
