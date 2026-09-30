package gbind

import (
	"unsafe"

	"github.com/velox-io/json/internal/gdec"
	"github.com/velox-io/json/native/ndec"
	"github.com/velox-io/json/vbind"
)

// Cursor. c.p rests on a token start: the next byte past whitespace, or
// len(src) at the end, where reads observe the 0x20 the native scanners see
// in their padding. Token starts are exactly the offsets the native
// structural index holds, so positions agree with the native binder.

func (c *binder) pos() uint64 { return uint64(c.p) }

func (c *binder) byteAt(o int) byte { return gdec.At(c.src, o) }

func (c *binder) peek() byte {
	if c.p < len(c.src) {
		return c.src[c.p]
	}
	return ' '
}

func (c *binder) eof() bool { return c.p >= len(c.src) }

// to rests the cursor on the first token start at or past o.
func (c *binder) to(o int) {
	// A token follows its predecessor directly or after one space, as in
	// `": "`; longer runs take the word-wise skip.
	if uint(o+1) < uint(len(c.src)) {
		if c.src[o] > ' ' {
			c.p = o
			return
		}
		if c.src[o] == ' ' && c.src[o+1] > ' ' {
			c.p = o + 1
			return
		}
	}
	c.p = gdec.SkipWS(c.src, o)
}

// next moves past the operator byte at the cursor.
func (c *binder) next() { c.to(c.p + 1) }

func (c *binder) accept(ch byte) bool {
	if c.peek() == ch {
		c.next()
		return true
	}
	return false
}

// skipToken moves past the token at the cursor without judging it. An
// unclosed string is the scan verdict's failure. Under the strict scan a
// string token's body holds the strict policy.
func (c *binder) skipToken() error {
	end, ok := gdec.TokenEnd(c.src, c.p)
	if !ok {
		return c.failNoPos(ndec.BindErrSyntax)
	}
	if c.strict && c.p < len(c.src) && c.src[c.p] == '"' && !gdec.ValidateBody(c.src, c.p+1, end-1) {
		return c.failNoPos(ndec.BindErrSyntax)
	}
	c.to(end)
	return nil
}

// expectColon mirrors SRC_EXPECT(':'), whose failure names no position.
func (c *binder) expectColon() error {
	if c.peek() != ':' {
		return c.fail(ndec.BindErrSyntax, 0)
	}
	c.next()
	return nil
}

// seek runs fn with the cursor at token start at, then restores it.
func (c *binder) seek(at int, fn func() error) error {
	saved := c.p
	c.p = at
	err := fn()
	c.p = saved
	return err
}

// closeErr reports a member or element followed by neither ',' nor the
// container's close, the token at the cursor: truncation at the end, and
// otherwise a syntax error naming the token after it, where the native
// cursor stands once it consumed the offending one.
func (c *binder) closeErr() error {
	if c.eof() {
		return c.fail(ndec.BindErrEOF, c.pos())
	}
	if err := c.skipToken(); err != nil {
		return err
	}
	return c.fail(ndec.BindErrSyntax, c.pos())
}

// Errors.

func (c *binder) fail(kind uint32, pos uint64) error {
	c.info = Error{Kind: kind, Pos: pos, TypeIdx: int(c.rootType)}
	return errAbort
}

func (c *binder) failType(kind uint32, pos uint64, typeIdx uint32) error {
	c.info = Error{Kind: kind, Pos: pos, TypeIdx: int(typeIdx)}
	return errAbort
}

func (c *binder) failNoPos(kind uint32) error {
	c.info = Error{Kind: kind, Pos: NoPos, TypeIdx: int(c.rootType)}
	return errAbort
}

// failValueOrEOF is BIND_ERR_VALUE_OR_EOF: a mismatch at the end sentinel
// is truncation.
func (c *binder) failValueOrEOF(kind uint32, ctr uint32) error {
	if c.eof() {
		return c.fail(ndec.BindErrEOF, c.pos())
	}
	return c.failType(kind, c.pos(), ctr)
}

func (c *binder) record(pos uint64) {
	if !c.mismatch {
		c.mismatch = true
		c.mismatchPos = pos
	}
}

func (c *binder) push() error {
	if c.depth+1 > maxDepth {
		return c.failNoPos(ndec.BindErrDepth)
	}
	c.depth++
	return nil
}

func (c *binder) pop() { c.depth-- }

// Type table.

func (c *binder) typ(ti uint32) *vbind.BindType { return &c.tt.Types[ti] }

func (c *binder) child(ti uint32) uint32 {
	return c.tt.Types[ti].ChildIndex(&c.tt.Types[0])
}

func (c *binder) size(ti uint32) uintptr { return uintptr(c.tt.TypeMeta[ti].Size) }

// hostField resolves a host struct field by JSON name.
func (c *binder) hostField(ti uint32, key string) *vbind.BindField {
	idx := vbind.LookupFind(c.pl.keys[ti].blob, key)
	if idx < 0 {
		return nil
	}
	first := c.typ(ti).StructFirstFieldIndex(&c.tt.Fields[0])
	return &c.tt.Fields[first+uint32(idx)]
}

// carve takes one zeroed element of slot class ci.
func (c *binder) carve(ci int32) unsafe.Pointer {
	p, _ := c.a.Carve(ci)
	return p
}

// newPointee carves a pointer's zeroed pointee.
func (c *binder) newPointee(ptrTi uint32) unsafe.Pointer {
	return c.carve(c.tt.Types[ptrTi].Pointer().AllocClass)
}
