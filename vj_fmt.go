package vjson

import (
	"bytes"
	stdjson "encoding/json"
	"math"
	"runtime"
	"sync"
	"unsafe"

	"github.com/velox-io/json/jerr"
	"github.com/velox-io/json/native/ndec"
)

// Compact appends to dst the compact form of src: insignificant
// whitespace is removed. Invalid JSON yields a *SyntaxError.
func Compact(dst *bytes.Buffer, src []byte) error {
	return reformat(dst, src, true, "", "")
}

// Indent appends to dst the indented form of src: each element in an
// object or array begins on a new line starting with prefix followed by
// one copy of indent per nesting level. Invalid JSON yields a *SyntaxError.
func Indent(dst *bytes.Buffer, src []byte, prefix, indent string) error {
	return reformat(dst, src, false, prefix, indent)
}

var fmtStatePool = sync.Pool{New: func() any { return new([ndec.FmtStateSize]byte) }}

func reformat(dst *bytes.Buffer, src []byte, compact bool, prefix, indent string) error {
	if !ndec.Available {
		return reformatStd(dst, src, compact, prefix, indent)
	}
	if len(src) > math.MaxUint32 {
		return jerr.NewSyntaxError("json: document exceeds the 4GB limit", 0)
	}

	grow := len(src) + 1
	if !compact {
		grow = 2*len(src) + len(prefix) + len(indent) + 64
	}

	// Loop-invariant call setup: a retry only repoints Dst/DstCap. Err and
	// DstLen are outputs the entry assigns on every run.
	var ctx ndec.FmtContext
	if len(src) > 0 {
		ctx.Src = unsafe.SliceData(src)
	}
	ctx.SrcLen = uintptr(len(src))
	if compact {
		ctx.Compact = 1
	} else {
		if len(prefix) > 0 {
			ctx.Prefix = unsafe.StringData(prefix)
		}
		ctx.PrefixLen = uint32(len(prefix))
		if len(indent) > 0 {
			ctx.Indent = unsafe.StringData(indent)
		}
		ctx.IndentLen = uint32(len(indent))
	}
	// ndec_fmt_run reinitializes the state on entry, so one scratch buffer
	// serves every retry.
	state := fmtStatePool.Get().(*[ndec.FmtStateSize]byte)
	ctx.State = unsafe.Pointer(state)
	defer func() {
		ctx.State = nil
		// The KeepAlives extend the borrowed buffers' lifetime past the
		// last native call; Put hands the scratch back once nothing pins it.
		runtime.KeepAlive(src)
		runtime.KeepAlive(prefix)
		runtime.KeepAlive(indent)
		runtime.KeepAlive(dst)
		fmtStatePool.Put(state)
	}()

	for {
		// Grow first so AvailableBuffer is dst's own tail: the native write
		// lands in place, and the Write below commits only the length.
		dst.Grow(grow)
		buf := dst.AvailableBuffer()[:grow]
		ctx.Dst = unsafe.SliceData(buf)
		ctx.DstCap = uintptr(grow)
		ctx.Run()
		switch ctx.Err {
		case 0:
			// buf is dst's unwritten tail, so this commits the length with a self-copy.
			dst.Write(buf[:int(ctx.DstLen)])
			return nil
		case ndec.FmtFull: // DstLen holds the exact needed size
			grow = int(ctx.DstLen)
		default:
			return fmtErr(int(ctx.Err), int(ctx.ErrPos))
		}
	}
}

func reformatStd(dst *bytes.Buffer, src []byte, compact bool, prefix, indent string) error {
	if compact {
		return stdjson.Compact(dst, src)
	}
	return stdjson.Indent(dst, src, prefix, indent)
}

func fmtErr(code, pos int) error {
	switch code {
	case ndec.ErrEOF:
		return jerr.NewSyntaxError("json: unexpected end of input", pos)
	case ndec.ErrDepth:
		return jerr.NewSyntaxError("json: exceeded max nesting depth", pos)
	case ndec.ErrKeyword:
		return jerr.NewSyntaxError("json: invalid literal", pos)
	case ndec.ErrTrailing:
		return jerr.NewSyntaxError("json: trailing data after top-level value", pos)
	default:
		return jerr.NewSyntaxError("json: invalid character", pos)
	}
}
