// Pure functions that write raw JSON bytes into Go memory.
//
// Used by BEGIN_PTR and GROW_SLICE yield handlers: the reactor passes raw
// token bytes to the driver, which allocates elements (slice or pointer)
// then calls these functions to decode the raw bytes into them.
//
// The reactor pre-unescapes strings into the scratch buffer, so string
// paths directly alias the raw bytes.
//
// Number parsing goes through strconv (not the native atof) because these
// paths still flow through yields. Once number parsing for ptr/slice
// elements moves to C, these functions will be removed.

package ndec

import (
	"fmt"
	"strconv"
	"unsafe"
)

// writeNumberFallback re-parses raw bytes into the target numeric kind and
// writes to dst. Used by BEGIN_PTR (scalar elem) and GROW_SLICE (slice elem)
// paths; driver receives raw bytes and writes directly to Go memory without
// going through yield NUMBER_* dispatch.
func writeNumberFallback(dst unsafe.Pointer, kind bindKind, raw []byte) error {
	rawStr := unsafe.String(unsafe.SliceData(raw), len(raw))

	switch kind {
	case bkInt8, bkInt16, bkInt32, bkInt64, bkInt:
		v, err := strconv.ParseInt(rawStr, 10, 64)
		if err != nil {
			// Number contains decimal point or exponent: try ParseFloat and narrow.
			f, ferr := strconv.ParseFloat(rawStr, 64)
			if ferr != nil {
				return fmt.Errorf("ndec: cannot parse %q as integer: %w", rawStr, err)
			}
			iv := int64(f)
			if float64(iv) != f {
				return fmt.Errorf("ndec: cannot represent %q as integer (lossy)", rawStr)
			}
			v = iv
		}
		switch kind {
		case bkInt8:
			if v < -128 || v > 127 {
				return rangeError(rawStr, kind)
			}
			*(*int8)(dst) = int8(v)
		case bkInt16:
			if v < -32768 || v > 32767 {
				return rangeError(rawStr, kind)
			}
			*(*int16)(dst) = int16(v)
		case bkInt32:
			if v < -2147483648 || v > 2147483647 {
				return rangeError(rawStr, kind)
			}
			*(*int32)(dst) = int32(v)
		case bkInt64, bkInt:
			*(*int64)(dst) = v
		}
		return nil

	case bkUint8, bkUint16, bkUint32, bkUint64, bkUint:
		v, err := strconv.ParseUint(rawStr, 10, 64)
		if err != nil {
			f, ferr := strconv.ParseFloat(rawStr, 64)
			if ferr != nil {
				return fmt.Errorf("ndec: cannot parse %q as unsigned: %w", rawStr, err)
			}
			if f < 0 {
				return fmt.Errorf("ndec: cannot store negative %q as unsigned", rawStr)
			}
			uv := uint64(f)
			if float64(uv) != f {
				return fmt.Errorf("ndec: cannot represent %q as unsigned (lossy)", rawStr)
			}
			v = uv
		}
		switch kind {
		case bkUint8:
			if v > 255 {
				return rangeError(rawStr, kind)
			}
			*(*uint8)(dst) = uint8(v)
		case bkUint16:
			if v > 65535 {
				return rangeError(rawStr, kind)
			}
			*(*uint16)(dst) = uint16(v)
		case bkUint32:
			if v > 4294967295 {
				return rangeError(rawStr, kind)
			}
			*(*uint32)(dst) = uint32(v)
		case bkUint64, bkUint:
			*(*uint64)(dst) = v
		}
		return nil

	case bkFloat32, bkFloat64:
		f, err := strconv.ParseFloat(rawStr, 64)
		if err != nil {
			return fmt.Errorf("ndec: cannot parse %q as float: %w", rawStr, err)
		}
		if kind == bkFloat32 {
			*(*float32)(dst) = float32(f)
		} else {
			*(*float64)(dst) = f
		}
		return nil

	default:
		return fmt.Errorf("ndec: yield number into unsupported kind %d", kind)
	}
}

func rangeError(raw string, kind bindKind) error {
	return fmt.Errorf("ndec: number %q out of range for kind %d", raw, kind)
}

// writeSliceElem writes raw bytes to a slice element at dst.
// The reactor has pre-unescaped strings into scratch (raw points into a
// scratch sub-range), so the string path directly aliases.
//
// JSON null elements never reach this function: the scalar_null hook on
// SLICE frames either takes the zero-value OK path (capacity sufficient)
// or the NEED_GROW + yfGrowNull yield path where handleGrowSliceYield
// zero-fills directly.
func writeSliceElem(dst unsafe.Pointer, kind bindKind, raw []byte, d *driverState) error {
	switch kind {
	case bkString:
		h := (*goStringHeader)(dst)
		if len(raw) > 0 {
			h.data = unsafe.Pointer(&raw[0])
		}
		h.len = uintptr(len(raw))
		return nil
	case bkBool:
		if len(raw) == 4 && raw[0] == 't' {
			*(*uint8)(dst) = 1
		} else if len(raw) == 5 && raw[0] == 'f' {
			*(*uint8)(dst) = 0
		} else {
			return fmt.Errorf("ndec: slice-bool unexpected raw %q", raw)
		}
		return nil
	default:
		return writeNumberFallback(dst, kind, raw)
	}
}
