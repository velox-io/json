package gbind

import (
	"errors"
	"strconv"
	"unsafe"

	"github.com/velox-io/json/internal/gdec"
	"github.com/velox-io/json/native/ndec"
)

// bindAny stores the value at token start p in the empty interface slot dst
// as float64 (or json.Number), string, bool, nil, []any, or map[string]any,
// laid out as the native any_value lays them, and returns the token start
// past it: numbers and strings box into the any slot classes, bools point
// at static words, an array boxes its []any header, and an object's map is
// the data word itself. A container is published before its contents bind.
func (c *binder) bindAny(s text, p int, dst unsafe.Pointer, ctr uint32) (int, error) {
	am := c.tt.Types[c.tt.AnyTypeIdx].AnyMeta(c.tt.AnyMetas)
	eface := (*[2]unsafe.Pointer)(dst)
	switch ch := s.peek(p); {
	case ch == '"':
		str, q, err := c.strAt(s, p)
		if err != nil {
			return q, err
		}
		box := c.carve(am.StringSlotClass)
		*(*string)(box) = str
		*eface = [2]unsafe.Pointer{am.StringType, box}
		return q, nil
	case ch == '-' || gdec.IsDigit(ch):
		if c.opt&ndec.BindOptUseNumber != 0 {
			str, q, err := c.numberTextAt(s, p, ctr)
			if err != nil {
				return q, err
			}
			box := c.carve(am.StringSlotClass)
			*(*string)(box) = str
			*eface = [2]unsafe.Pointer{am.NumberType, box}
			return q, nil
		}
		f, end, ok, exact := gdec.FloatToken(unsafe.Slice((*byte)(s.b), s.n), p)
		if !ok {
			return p, c.failType(ndec.BindErrTypeMismatch, uint64(p), ctr)
		}
		if gdec.IsNonDelim(s.peek(end)) {
			return p, c.fail(ndec.BindErrSyntax, uint64(p))
		}
		if !exact {
			var err error
			if f, err = strconv.ParseFloat(unsafe.String(s.ptr(p), end-p), 64); err != nil {
				// A range overflow stores the ±Inf box before the mismatch
				// is reported, like the native any_value.
				if errors.Is(err, strconv.ErrRange) {
					box := c.carve(am.Float64SlotClass)
					*(*float64)(box) = f
					*eface = [2]unsafe.Pointer{am.Float64Type, box}
				}
				return p, c.failType(ndec.BindErrTypeMismatch, uint64(p), ctr)
			}
		}
		box := c.carve(am.Float64SlotClass)
		*(*float64)(box) = f
		*eface = [2]unsafe.Pointer{am.Float64Type, box}
		return s.skip(end), nil
	case ch == 't' || ch == 'f' || ch == 'n':
		end, ok := s.atomEnd(p, ch)
		if !ok {
			return p, c.fail(ndec.BindErrSyntax, uint64(p))
		}
		switch ch {
		case 't':
			*eface = [2]unsafe.Pointer{am.BoolType, unsafe.Pointer(am.StaticTrue)}
		case 'f':
			*eface = [2]unsafe.Pointer{am.BoolType, unsafe.Pointer(am.StaticFalse)}
		default:
			*eface = [2]unsafe.Pointer{}
		}
		return s.skip(end), nil
	case ch == '[':
		if err := c.push(); err != nil {
			return p, err
		}
		p = s.skip(p + 1)
		ti := uint32(am.SliceAnyTypeIdx)
		box := c.carve(am.SliceSlotClass)
		*eface = [2]unsafe.Pointer{am.SliceType, box}
		if s.peek(p) == ']' {
			c.emptyArray(box, ti)
			c.pop()
			return s.skip(p + 1), nil
		}
		return c.arrayElems(s, p, box, ti)
	case ch == '{':
		if err := c.push(); err != nil {
			return p, err
		}
		p = s.skip(p + 1)
		ti := uint32(am.MapAnyTypeIdx)
		*eface = [2]unsafe.Pointer{am.MapType, nil}
		m := c.openMap(unsafe.Pointer(&eface[1]), ti)
		if s.peek(p) == '}' {
			c.pop()
			return s.skip(p + 1), nil
		}
		return c.mapEntries(s, p, m, ti)
	}
	c.p = p
	return p, c.failValueOrEOF(ndec.BindErrSyntax, ctr)
}
