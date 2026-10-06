package bind

import (
	"errors"
	"fmt"
	"io"
	"reflect"
	"strings"
	"unsafe"

	"github.com/velox-io/json/decode"
	"github.com/velox-io/json/internal/gdec"
	"github.com/velox-io/json/jerr"
	"github.com/velox-io/json/native/ndec"
	"github.com/velox-io/json/vbind"
)

type (
	SyntaxError           = decode.SyntaxError
	UnmarshalTypeError    = decode.UnmarshalTypeError
	InvalidUnmarshalError = decode.InvalidUnmarshalError
)

// TapeBindUnsupportedError reports the first target position rejected by the
// native tape binder.
type TapeBindUnsupportedError struct {
	Pos  *vbind.TapeBindUnsupportedPos
	Type reflect.Type
}

func (e *TapeBindUnsupportedError) Error() string {
	typeStr := "<unknown>"
	if e.Type != nil {
		typeStr = e.Type.String()
	}
	return fmt.Sprintf("vjson: tape-bind cannot bind %s at %s (%s); use Unmarshal or dom.Value.Interface() instead",
		typeStr, e.Pos.Path, e.Pos.Reason)
}

// ErrZeroCopyValue reports that typed binding would extend a zero-copy Value's
// source immutability and reuse restrictions into the output.
var ErrZeroCopyValue = errors.New("vjson: cannot bind a zero-copy Value; re-parse without vopt.ZeroCopy(true)")

// ErrZeroCopyTypedTree reports that an explicit ZeroCopy(true) demand hit
// a destination tree carrying value.Value or poly fields. Their content flows
// through the tape machinery, which stays arena-backed, so zero-copy binds
// typed trees only; the default silently falls back to the copying parse on
// those trees.
var ErrZeroCopyTypedTree = errors.New("vjson: ZeroCopy(true) supports typed trees only; value.Value and poly fields stay arena-backed")

// bindErrInfo is the engine-neutral error payload: native fills it from the
// yield, and the Go engine returns it as a gbind.Error of the same layout.
// Pos is ^0 when the error has no source position. TypeIdx names the type
// the error reports against; Target is the variant host for discriminator
// errors. EndDelta is the distance from Pos to one past the offending token,
// recorded by the engine beside the position, zero when unknown.
type bindErrInfo struct {
	Kind     uint32
	Detail   uint32
	Pos      uint64
	TypeIdx  int
	Target   unsafe.Pointer
	EndDelta uint32
}

func machineErrInfo(m *ndec.BindMachine) bindErrInfo {
	typeIdx := int(m.Core.CurType.TypeIdx)
	endDelta := uint32(0)
	// A promoted mismatch reports the destination that rejected the value,
	// which the walk recorded beside the kind. Everything else keeps the
	// container current at the yield.
	if m.Yield.Arg0 == ndec.BindErrTypeMismatch && m.Core.FirstErrorTypeIdx != 0xFFFFFFFF {
		typeIdx = int(m.Core.FirstErrorTypeIdx)
		endDelta = m.Core.FirstErrorEndDelta
	}
	return bindErrInfo{
		Kind:     m.Yield.Arg0,
		Detail:   m.Yield.Arg1,
		Pos:      m.Yield.FirstErrorPos,
		TypeIdx:  typeIdx,
		Target:   m.Yield.Target,
		EndDelta: endDelta,
	}
}

// mkBindErr translates an error payload. srcBase is the document offset of
// src[0], zero for the contiguous engine's whole-document view and the
// window base for the streaming engine. valueRooted reports that src spans
// exactly one complete value starting at its root, which is what the
// pointer rebuild of a mismatch site requires: the contiguous and Go-engine
// drivers pass whole documents or whole values, while the streaming window
// slides mid-value and cannot back the rebuild. EOF errors wrap
// io.ErrUnexpectedEOF for errors.Is.
func mkBindErr(p *Parser, e bindErrInfo, src []byte, srcBase uint64, valueRooted bool) error {
	kind := e.Kind
	pos, hasPos := e.Pos, e.Pos != ^uint64(0)
	if !hasPos {
		pos = 0
	}
	switch kind {
	case ndec.BindErrSyntax:
		return jerr.NewSyntaxError("bind: syntax error", int(pos))
	case ndec.BindErrEOF:
		return jerr.NewSyntaxErrorWrap("bind: unexpected end of input", int(pos), io.ErrUnexpectedEOF)
	case ndec.BindErrDepth:
		return jerr.NewSyntaxError("bind: max depth exceeded", int(pos))
	case ndec.BindErrUTF8:
		return jerr.NewSyntaxError("bind: invalid UTF-8", int(pos))
	case ndec.BindErrTrailing:
		return jerr.NewSyntaxError("bind: trailing data after value", int(pos))
	case ndec.BindErrTypeMismatch:
		var rt reflect.Type
		idx := e.TypeIdx
		if idx < len(p.tt.ReflectTypes) {
			rt = p.tt.ReflectTypes[idx]
		}
		value := "json"
		if hasPos {
			value = jsonValueName(src, pos, srcBase)
		}
		e2 := &UnmarshalTypeError{
			Value:  value,
			Type:   rt,
			Offset: int64(pos),
		}
		if !hasPos {
			return e2
		}
		// The reported offset points one past the offending token rather
		// than at it, matching encoding/json. The engine recorded the
		// distance beside the position; zero leaves the token start.
		if e.EndDelta != 0 {
			e2.Offset = int64(pos + uint64(e.EndDelta))
		} else if local := int(pos - srcBase); pos >= srcBase && local < len(src) {
			// The recording window held the whole token but carried no
			// distance, as with the Go engine, whose window still spans the
			// value: scan the extent directly.
			if end, ok := gdec.TokenEnd(src, local); ok {
				e2.Offset = int64(srcBase + uint64(end))
			}
		}
		// The rebuilt identity of the mismatch site names the field path,
		// the leaf type that rejected the value, and the ,string wording.
		// Any part the rebuild cannot supply keeps the fallbacks above.
		// It runs only over a value-rooted source: a window that began
		// mid-value would make the walk resolve the pointer against the
		// wrong root.
		if valueRooted {
			if mc, ok := p.rebuildMismatchContext(src, srcBase, pos); ok {
				if mc.leaf < uint32(len(p.tt.ReflectTypes)) {
					e2.Type = p.tt.ReflectTypes[mc.leaf]
				}
				if len(mc.tokens) > 0 {
					if p.tt.Root < uint32(len(p.tt.ReflectTypes)) {
						e2.Struct = p.tt.ReflectTypes[p.tt.Root].Name()
					}
					e2.Field = strings.Join(mc.tokens, ".")
				}
				e2.Offset = int64(mc.tokenEnd)
				e2.Value, e2.Err = mismatchValueName(value, mc, &p.tt.Types[mc.leaf])
			}
		}
		return e2
	case ndec.BindErrUnknownField:
		// Name the struct the offending key was rejected by. FirstErrorPos carries
		// the source offset when the error came from the JSON path.
		var rt reflect.Type
		if idx := e.TypeIdx; idx < len(p.tt.ReflectTypes) {
			rt = p.tt.ReflectTypes[idx]
		}
		return &UnmarshalTypeError{Value: "unknown_field", Type: rt, Offset: int64(pos)}
	case ndec.BindErrUnsupportedTag:
		return jerr.NewSyntaxError("bind: unsupported target type on this bind path", int(pos))
	case ndec.BindErrVariantUnknownDisc, ndec.BindErrVariantMissingDisc:
		return mkVariantErr(p, e, pos)
	case ndec.BindErrKindofUnregistered:
		return mkKindofErr(p, e, pos)
	case ndec.BindErrKindofColdCase:
		msg := "case target type not supported (use a concrete type or any)"
		if kind := kindofName(e.Detail); kind != "" {
			msg = "case " + kind + " target type not supported (use a concrete type or any)"
		}
		return &KindofError{Host: bindErrorHost(p, e), Message: msg, Pos: int64(pos)}
	case ndec.BindErrVariantColdCase:
		return &VariantError{Host: bindErrorHost(p, e), VariantIdx: uint16(e.Detail),
			Message: "case target type not supported (use a concrete type or any)", Pos: int64(pos)}
	default:
		return jerr.NewSyntaxError("bind: native error", int(pos))
	}
}

// mkVariantErr uses the variant index and host pointer stashed in the yield to
// report the discriminator value.
func mkVariantErr(p *Parser, e bindErrInfo, pos uint64) error {
	variantIdx := uint16(e.Detail)
	host := bindErrorHost(p, e)
	msg := "unknown discriminator value"
	if e.Kind == ndec.BindErrVariantMissingDisc {
		msg = "missing discriminator"
	} else if int(variantIdx) < len(p.tt.Polys) {
		discOff := uintptr(p.tt.Polys[variantIdx].DiscFieldOff)
		hostPtr := e.Target
		if hostPtr != nil {
			s := readDiscFromHost(hostPtr, discOff)
			if s == "" {
				msg = "missing discriminator"
			} else {
				msg = "unknown discriminator value " + truncateForErr(s)
			}
		}
	}
	return &VariantError{Host: host, VariantIdx: variantIdx, Message: msg, Pos: int64(pos)}
}

// mkKindofErr builds the user-facing error for kindof resolution failures.
// Arg1 carries the stable kind ordinal independently of source availability.
func mkKindofErr(p *Parser, e bindErrInfo, pos uint64) error {
	msg := "unregistered JSON kind"
	if kind := kindofName(e.Detail); kind != "" {
		msg += " " + kind
	}
	return &KindofError{Host: bindErrorHost(p, e), Message: msg, Pos: int64(pos)}
}

func bindErrorHost(p *Parser, e bindErrInfo) string {
	idx := e.TypeIdx
	if idx >= 0 && idx < len(p.tt.ReflectTypes) {
		return p.tt.ReflectTypes[idx].String()
	}
	return ""
}

func kindofName(kind uint32) string {
	names := [...]string{"bool", "number", "string", "array", "object"}
	if kind < uint32(len(names)) {
		return names[kind]
	}
	return ""
}

// readDiscFromHost reads the Go string at hostPtr+discOff (the variant's
// vdisc field). Returns "" if the pointer is nil or length is 0. The string
// header layout (ptr, len) matches runtime.StringHeader.
func readDiscFromHost(hostPtr unsafe.Pointer, discOff uintptr) string {
	if hostPtr == nil {
		return ""
	}
	base := unsafe.Add(hostPtr, discOff)
	sp := *(*unsafe.Pointer)(base)
	ln := *(*uint64)(unsafe.Add(base, unsafe.Sizeof(unsafe.Pointer(nil))))
	if sp == nil || ln == 0 {
		return ""
	}
	return unsafe.String((*byte)(sp), ln)
}

// truncateForErr bounds discriminator text included in an error.
func truncateForErr(s string) string {
	const max = 32
	if len(s) <= max {
		return s
	}
	return s[:max] + "...(truncated)"
}

// VariantError reports a variant resolution failure: an unknown or missing
// discriminator value, or a selected case whose target type the binder cannot
// construct. Raised by the C-side tape-bind sub-routine when it cannot resolve
// a case from the discriminator.
type VariantError struct {
	Host       string // host Go type name (empty if not derivable)
	VariantIdx uint16 // variant's index into TypeTree.Polys
	Message    string
	Pos        int64 // source byte offset (0 = no position)
}

func (e *VariantError) Error() string {
	if e.Host != "" {
		return "bind: variant " + e.Message + " (host " + e.Host + ")"
	}
	return "bind: variant " + e.Message
}

// KindofError reports a kindof resolution failure: the JSON value's kind has no
// registered case, or the selected case's target type is one the binder cannot
// construct. The JSON path may report the field value's source offset; tape and
// phase2 paths report no position.
type KindofError struct {
	Host    string // host Go type name (empty if not derivable)
	Message string
	Pos     int64 // source byte offset (0 = no position)
}

func (e *KindofError) Error() string {
	if e.Host != "" {
		return "bind: kindof " + e.Message + " (host " + e.Host + ")"
	}
	return "bind: kindof " + e.Message
}

// jsonValueName maps the JSON byte at the absolute document offset pos to the
// value category string. The position may fall outside src, for example a
// recorded skip error whose window the driver already retired.
func jsonValueName(src []byte, pos, srcBase uint64) string {
	if pos < srcBase {
		return "json"
	}
	local := int(pos - srcBase)
	if local >= len(src) {
		return "json"
	}
	switch src[local] {
	case 'n':
		return "null"
	case 't', 'f':
		return "bool"
	case '"':
		return "string"
	case '-', '0', '1', '2', '3', '4', '5', '6', '7', '8', '9':
		return "number"
	case '{':
		return "object"
	case '[':
		return "array"
	}
	return "json"
}
