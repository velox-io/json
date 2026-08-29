package bind

import (
	"errors"
	"fmt"
	"io"
	"reflect"
	"unsafe"

	"github.com/velox-io/json/decode"
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
var ErrZeroCopyValue = errors.New("vjson: cannot bind a zero-copy Value; re-parse without option.WithZeroCopy")

// mkBindErr translates the native yield payload. EOF errors wrap
// io.ErrUnexpectedEOF for errors.Is.
func mkBindErr(p *Parser, m *ndec.BindMachine, src []byte) error {
	kind := m.Yield.Arg0
	pos, hasPos := bindErrorPos(m)
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
		idx := int(m.Core.CurType.TypeIdx)
		if idx < len(p.tt.ReflectTypes) {
			rt = p.tt.ReflectTypes[idx]
		}
		value := "json"
		if hasPos {
			value = jsonValueName(src, pos)
		}
		return &UnmarshalTypeError{
			Value:  value,
			Type:   rt,
			Offset: int64(pos),
		}
	case ndec.BindErrUnknownField:
		// Name the struct the offending key was rejected by. FirstErrorPos carries
		// the source offset when the error came from the JSON path.
		var rt reflect.Type
		if idx := int(m.Core.CurType.TypeIdx); idx < len(p.tt.ReflectTypes) {
			rt = p.tt.ReflectTypes[idx]
		}
		return &UnmarshalTypeError{Value: "unknown_field", Type: rt, Offset: int64(pos)}
	case ndec.BindErrUnsupportedTag:
		return jerr.NewSyntaxError("bind: field tag option not yet supported", int(pos))
	case ndec.BindErrVariantUnknownDisc, ndec.BindErrVariantMissingDisc:
		return mkVariantErr(p, m, kind, pos)
	case ndec.BindErrKindofUnregistered:
		return mkKindofErr(p, m, pos)
	default:
		return jerr.NewSyntaxError("bind: native error", int(pos))
	}
}

// mkVariantErr uses the variant index and host pointer stashed in the yield to
// report the discriminator value.
func mkVariantErr(p *Parser, m *ndec.BindMachine, kind, pos uint32) error {
	variantIdx := uint16(m.Yield.Arg1)
	host := bindErrorHost(p, m)
	msg := "unknown discriminator value"
	if kind == ndec.BindErrVariantMissingDisc {
		msg = "missing discriminator"
	} else if int(variantIdx) < len(p.tt.Variants) {
		vt := &p.tt.Variants[variantIdx]
		discOff := uintptr(vt.DiscFieldOff)
		hostPtr := m.Yield.Target
		if hostPtr != nil {
			s := readDiscFromHost(hostPtr, discOff)
			if s == "" {
				msg = "missing discriminator"
			} else {
				msg = "unknown discriminator value " + truncateForErr(s)
			}
		}
	}
	return &VariantError{Host: host, VariantIdx: variantIdx, Message: msg, Pos: pos}
}

// mkKindofErr builds the user-facing error for kindof resolution failures.
// Arg1 carries the stable kind ordinal independently of source availability.
func mkKindofErr(p *Parser, m *ndec.BindMachine, pos uint32) error {
	msg := "unregistered JSON kind"
	if kind := kindofName(m.Yield.Arg1); kind != "" {
		msg += " " + kind
	}
	return &KindofError{Host: bindErrorHost(p, m), Message: msg, Pos: pos}
}

func bindErrorPos(m *ndec.BindMachine) (uint32, bool) {
	if m.Yield.FirstErrorPos == ^uint32(0) {
		return 0, false
	}
	return m.Yield.FirstErrorPos, true
}

func bindErrorHost(p *Parser, m *ndec.BindMachine) string {
	idx := int(m.Core.CurType.TypeIdx)
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

// VariantError reports a variant resolution failure (unknown or missing
// discriminator value). Raised by the C-side tape-bind sub-routine when it
// cannot resolve a case from the discriminator.
type VariantError struct {
	Host       string // host Go type name (empty if not derivable)
	VariantIdx uint16 // index into TypeTree.Variants
	Message    string
	Pos        uint32 // source byte offset (0 = no position)
}

func (e *VariantError) Error() string {
	if e.Host != "" {
		return "bind: variant " + e.Message + " (host " + e.Host + ")"
	}
	return "bind: variant " + e.Message
}

// KindofError reports a kindof resolution failure: the JSON value's kind has no
// registered case. The JSON path may report the field value's source offset;
// tape and phase2 paths report no position.
type KindofError struct {
	Host    string // host Go type name (empty if not derivable)
	Message string
	Pos     uint32 // source byte offset (0 = no position)
}

func (e *KindofError) Error() string {
	if e.Host != "" {
		return "bind: kindof " + e.Message + " (host " + e.Host + ")"
	}
	return "bind: kindof " + e.Message
}

// jsonValueName maps the JSON byte at src[pos] to the value category string
func jsonValueName(src []byte, pos uint32) string {
	if int(pos) >= len(src) {
		return "json"
	}
	switch src[pos] {
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
