package ndec

import (
	"bytes"
	stdjson "encoding/json"
	"errors"
	"strings"
	"unsafe"

	"github.com/velox-io/json/internal/gdec"
)

// Pure-Go bodies of the stateless entries, taken when the blob is not
// mapped. Each reads and writes the same context the native entry does, so
// callers are unaware of the switch.

func bytesAt(p *byte, n uintptr) []byte {
	if p == nil {
		return nil
	}
	return unsafe.Slice(p, n)
}

func domViews(c *DOMContext) (src []byte, idx []uint32, tape []uint64, str []byte) {
	src = bytesAt(c.Src, c.SrcLen)
	if c.Structural != nil {
		idx = unsafe.Slice(c.Structural, c.StructuralCap)
	}
	if c.Tape != nil {
		tape = unsafe.Slice(c.Tape, c.TapeCap)
	}
	str = bytesAt(c.StrArena, c.StrArenaCap)
	return
}

func goDOMParseCounted(c *DOMContext) {
	if c.SrcLen == 0 {
		c.Err = -1
		return
	}
	src, idx, _, _ := domViews(c)
	mode := gdec.ScanLax
	if c.ScanStrict != 0 {
		mode = gdec.ScanStrict
	}
	n, scalars, ok := gdec.Scan(src, idx, mode)
	if !ok {
		c.Err = -1
		return
	}
	c.NStructural = uint32(n)
	c.TapeNeed = uint32(gdec.TapeNeed(n, scalars))
	if uintptr(c.TapeNeed) > c.TapeCap {
		c.Err = DomTapeFull
		return
	}
	goDOMBuild(c)
}

func goDOMBuild(c *DOMContext) {
	src, idx, tape, str := domViews(c)
	tl, su, ok := gdec.BuildTape(src, idx, int(c.NStructural), tape, str, c.StrMode == 1)
	c.TapeLen, c.StrUsed = uintptr(tl), uintptr(su)
	c.Err = 0
	if !ok {
		c.Err = -1
	}
}

func goValid(c *ValidContext) {
	src := bytesAt(c.Src, c.SrcLen)
	idx := unsafe.Slice(c.Structural, c.StructuralCap)
	c.Err = ValidErrInvalid
	if len(src) == 0 {
		return
	}
	if n, _, ok := gdec.Scan(src, idx, gdec.ScanCtl); ok && gdec.ValidWalk(src, idx, n) {
		c.Err = 0
	}
}

// goFmtParse reformats through encoding/json, whose Compact and Indent the
// native formatter was built to match, and maps its errors onto the entry's
// exit codes.
func goFmtParse(c *FmtContext) {
	src := bytesAt(c.Src, c.SrcLen)
	var out bytes.Buffer
	var err error
	if c.Compact != 0 {
		err = stdjson.Compact(&out, src)
	} else {
		prefix := unsafe.String(c.Prefix, c.PrefixLen)
		indent := unsafe.String(c.Indent, c.IndentLen)
		err = stdjson.Indent(&out, src, prefix, indent)
	}
	if err != nil {
		c.Err, c.ErrPos = fmtErrCode(err, len(src))
		return
	}
	c.DstLen = uintptr(out.Len())
	if c.DstLen > c.DstCap {
		c.Err = FmtFull
		return
	}
	copy(bytesAt(c.Dst, c.DstCap), out.Bytes())
	c.Err = 0
}

func fmtErrCode(err error, srcLen int) (int32, uint32) {
	var se *stdjson.SyntaxError
	if !errors.As(err, &se) {
		return ErrSyntax, 0
	}
	pos := uint32(max(se.Offset-1, 0))
	switch msg := se.Error(); {
	case strings.Contains(msg, "unexpected end"):
		return ErrEOF, uint32(srcLen)
	case strings.Contains(msg, "after top-level value"):
		return ErrTrailing, pos
	case strings.Contains(msg, "in literal"):
		return ErrKeyword, pos
	case strings.Contains(msg, "exceeded max depth"):
		return ErrDepth, pos
	}
	return ErrSyntax, pos
}
