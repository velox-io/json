package gbind

import (
	"errors"
	"strconv"

	"github.com/velox-io/json/internal/valueabi"
)

// errZeroSpanDouble rejects a tape double whose arena text is empty: every
// parser-produced double retains its token, so a zero span can only come from
// a corrupt tape, which the native tape binder rejects as well.
var errZeroSpanDouble = errors.New("vjson: value tape double carries no decimal text")

// ValueText renders a Value as the JSON the engine binds for UnmarshalValue.
// It differs from Value.MarshalJSON in one respect: a binary double always
// renders with a fraction or exponent, so an integer target rejects it as
// the tape walker does even when its value is whole.
func ValueText(desc *valueabi.Descriptor) ([]byte, error) {
	var buf []byte
	if err := appendValueText(&buf, desc, int(desc.Tidx)); err != nil {
		return nil, err
	}
	return buf, nil
}

func appendValueText(buf *[]byte, desc *valueabi.Descriptor, idx int) error {
	switch desc.TagAt(idx) {
	case valueabi.TagNull:
		*buf = append(*buf, "null"...)
	case valueabi.TagTrue:
		*buf = append(*buf, "true"...)
	case valueabi.TagFalse:
		*buf = append(*buf, "false"...)
	case valueabi.TagInt64:
		*buf = strconv.AppendInt(*buf, desc.Int64At(idx), 10)
	case valueabi.TagUint64:
		*buf = strconv.AppendUint(*buf, desc.Uint64At(idx), 10)
	case valueabi.TagDouble:
		// The arena text keeps the source spelling, so a float32 target
		// parses it once at binary32 precision as the tape walker does.
		// The text always carries a fraction or exponent, which keeps
		// integer targets rejecting it.
		text := desc.StringAt(idx)
		if len(text) == 0 {
			return errZeroSpanDouble
		}
		*buf = append(*buf, text...)
	case valueabi.TagNumRaw:
		*buf = append(*buf, desc.NumRawAt(idx)...)
	case valueabi.TagStrRaw, valueabi.TagStrFree:
		*buf = append(*buf, '"')
		*buf = append(*buf, desc.ScalarStringAt(idx)...)
		*buf = append(*buf, '"')
	case valueabi.TagString:
		*buf = appendQuoted(*buf, desc.ScalarStringAt(idx))
	case valueabi.TagArrBeg:
		*buf = append(*buf, '[')
		end := desc.ContainerEnd(idx)
		for cur, first := desc.SkipSeams(idx+1), true; cur < end; cur, first = desc.Skip(cur), false {
			if !first {
				*buf = append(*buf, ',')
			}
			if err := appendValueText(buf, desc, cur); err != nil {
				return err
			}
		}
		*buf = append(*buf, ']')
	case valueabi.TagObjBeg:
		*buf = append(*buf, '{')
		end := desc.ContainerEnd(idx)
		for cur, first := desc.SkipSeams(idx+1), true; cur < end; cur, first = desc.Skip(cur+1), false {
			if !first {
				*buf = append(*buf, ',')
			}
			*buf = appendQuoted(*buf, desc.ScalarStringAt(cur))
			*buf = append(*buf, ':')
			if err := appendValueText(buf, desc, cur+1); err != nil {
				return err
			}
		}
		*buf = append(*buf, '}')
	}
	return nil
}

// appendQuoted writes s as a JSON string. Bytes outside the escapes pass
// through verbatim, as the raw decode policy produced them.
func appendQuoted(dst, s []byte) []byte {
	const hex = "0123456789abcdef"
	dst = append(dst, '"')
	for _, c := range s {
		switch {
		case c == '"' || c == '\\':
			dst = append(dst, '\\', c)
		case c < 0x20:
			dst = append(dst, '\\', 'u', '0', '0', hex[c>>4], hex[c&15])
		default:
			dst = append(dst, c)
		}
	}
	return append(dst, '"')
}
