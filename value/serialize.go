package value

import (
	"strconv"

	"github.com/velox-io/json/internal/valueabi"
)

func tapeToJSON(v *Value) []byte {
	var buf []byte
	appendTapeJSON(&buf, &v.desc, int(v.desc.Tidx))
	return buf
}

func appendTapeJSON(buf *[]byte, desc *valueabi.Descriptor, idx int) {
	switch desc.TagAt(idx) {
	case valueabi.TagNull:
		*buf = append(*buf, 'n', 'u', 'l', 'l')
	case valueabi.TagTrue:
		*buf = append(*buf, 't', 'r', 'u', 'e')
	case valueabi.TagFalse:
		*buf = append(*buf, 'f', 'a', 'l', 's', 'e')
	case valueabi.TagInt64:
		*buf = strconv.AppendInt(*buf, desc.Int64At(idx), 10)
	case valueabi.TagUint64:
		*buf = strconv.AppendUint(*buf, desc.Uint64At(idx), 10)
	case valueabi.TagDouble:
		*buf = strconv.AppendFloat(*buf, desc.DoubleAt(idx), 'g', -1, 64)
	case valueabi.TagNumRaw:
		*buf = append(*buf, desc.NumRawAt(idx)...)
	case valueabi.TagStrRaw, valueabi.TagStrFree:
		*buf = append(*buf, '"')
		*buf = append(*buf, desc.ScalarStringAt(idx)...)
		*buf = append(*buf, '"')
	case valueabi.TagString:
		*buf = appendJSONString(*buf, desc.ScalarStringAt(idx))
	case valueabi.TagArrBeg:
		*buf = append(*buf, '[')
		end := desc.ContainerEnd(idx)
		cur := desc.SkipSeams(idx + 1)
		first := true
		for cur < end {
			if !first {
				*buf = append(*buf, ',')
			}
			first = false
			appendTapeJSON(buf, desc, cur)
			cur = desc.Skip(cur)
		}
		*buf = append(*buf, ']')
	case valueabi.TagObjBeg:
		*buf = append(*buf, '{')
		end := desc.ContainerEnd(idx)
		cur := desc.SkipSeams(idx + 1)
		first := true
		for cur < end {
			if !first {
				*buf = append(*buf, ',')
			}
			first = false
			*buf = appendJSONString(*buf, desc.ScalarStringAt(cur))
			*buf = append(*buf, ':')
			valIdx := cur + 1
			appendTapeJSON(buf, desc, valIdx)
			cur = desc.Skip(valIdx)
		}
		*buf = append(*buf, '}')
	}
}

func appendJSONString(dst, src []byte) []byte {
	dst = append(dst, '"')
	for _, c := range src {
		switch c {
		case '"':
			dst = append(dst, '\\', '"')
		case '\\':
			dst = append(dst, '\\', '\\')
		case '\b':
			dst = append(dst, '\\', 'b')
		case '\f':
			dst = append(dst, '\\', 'f')
		case '\n':
			dst = append(dst, '\\', 'n')
		case '\r':
			dst = append(dst, '\\', 'r')
		case '\t':
			dst = append(dst, '\\', 't')
		default:
			if c < 0x20 {
				dst = append(dst, '\\', 'u', '0', '0', hexChar(c>>4), hexChar(c&0xF))
			} else {
				dst = append(dst, c)
			}
		}
	}
	return append(dst, '"')
}

func hexChar(n byte) byte {
	if n < 10 {
		return '0' + n
	}
	return 'a' - 10 + n
}
