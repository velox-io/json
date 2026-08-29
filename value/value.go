// Package value provides lazy access to parsed JSON values.
package value

import (
	"strconv"

	"github.com/velox-io/json/internal/valueabi"
)

// Value is a compact, immutable view of a parsed JSON value.
type Value struct {
	desc valueabi.Descriptor
}

// Kind classifies a JSON value.
type Kind uint8

const (
	// KindInvalid identifies an empty or malformed value.
	KindInvalid Kind = iota
	KindNull
	KindBool
	KindNumber
	KindString
	KindArray
	KindObject
)

// String returns the kind name.
func (k Kind) String() string {
	switch k {
	case KindNull:
		return "null"
	case KindBool:
		return "bool"
	case KindNumber:
		return "number"
	case KindString:
		return "string"
	case KindArray:
		return "array"
	case KindObject:
		return "object"
	}
	return "invalid"
}

func tagToKind(tag byte) Kind {
	if valueabi.IsStringTag(tag) {
		return KindString
	}
	switch tag {
	case valueabi.TagNull:
		return KindNull
	case valueabi.TagTrue, valueabi.TagFalse:
		return KindBool
	case valueabi.TagInt64, valueabi.TagUint64, valueabi.TagDouble, valueabi.TagNumRaw:
		return KindNumber
	case valueabi.TagArrBeg:
		return KindArray
	case valueabi.TagObjBeg:
		return KindObject
	}
	return KindInvalid
}

func (v *Value) hasTape() bool {
	return v.desc.HasTape()
}

// Exists reports whether v holds a value.
func (v *Value) Exists() bool {
	return v.hasTape()
}

// Type reports the JSON kind held by v.
func (v *Value) Type() Kind {
	if !v.hasTape() {
		return KindInvalid
	}
	return tagToKind(v.desc.TagAt(int(v.desc.Tidx)))
}

// Valid reports whether v holds a parsed JSON value.
func (v *Value) Valid() bool {
	return v.hasTape()
}

// Len returns the element or member count of a container.
func (v *Value) Len() int {
	if !v.hasTape() {
		return 0
	}
	idx := int(v.desc.Tidx)
	tag := v.desc.TagAt(idx)
	if tag == valueabi.TagArrBeg || tag == valueabi.TagObjBeg {
		return v.desc.ContainerCount(idx)
	}
	return 0
}

// Str returns the decoded JSON string held by v.
func (v *Value) Str() (string, bool) {
	if !v.hasTape() {
		return "", false
	}
	idx := int(v.desc.Tidx)
	if !valueabi.IsStringTag(v.desc.TagAt(idx)) {
		return "", false
	}
	return string(v.desc.ScalarStringAt(idx)), true
}

// Int returns v as an int64 when its value is integral and in range.
func (v *Value) Int() (int64, bool) {
	if !v.hasTape() {
		return 0, false
	}
	idx := int(v.desc.Tidx)
	switch v.desc.TagAt(idx) {
	case valueabi.TagInt64:
		return v.desc.Int64At(idx), true
	case valueabi.TagUint64:
		u := v.desc.Uint64At(idx)
		if u > uint64(1<<63-1) {
			return 0, false
		}
		return int64(u), true
	case valueabi.TagDouble:
		f := v.desc.DoubleAt(idx)
		if f != float64(int64(f)) {
			return 0, false
		}
		return int64(f), true
	case valueabi.TagNumRaw:
		n, err := strconv.ParseInt(string(v.desc.NumRawAt(idx)), 10, 64)
		return n, err == nil
	}
	return 0, false
}

// Uint returns v as a uint64 when its value is integral and in range.
func (v *Value) Uint() (uint64, bool) {
	if !v.hasTape() {
		return 0, false
	}
	idx := int(v.desc.Tidx)
	switch v.desc.TagAt(idx) {
	case valueabi.TagUint64:
		return v.desc.Uint64At(idx), true
	case valueabi.TagInt64:
		n := v.desc.Int64At(idx)
		if n < 0 {
			return 0, false
		}
		return uint64(n), true
	case valueabi.TagDouble:
		f := v.desc.DoubleAt(idx)
		if f < 0 || f != float64(uint64(f)) {
			return 0, false
		}
		return uint64(f), true
	case valueabi.TagNumRaw:
		n, err := strconv.ParseUint(string(v.desc.NumRawAt(idx)), 10, 64)
		return n, err == nil
	}
	return 0, false
}

// Float returns the number held by v as a float64.
func (v *Value) Float() (float64, bool) {
	if !v.hasTape() {
		return 0, false
	}
	idx := int(v.desc.Tidx)
	switch v.desc.TagAt(idx) {
	case valueabi.TagDouble:
		return v.desc.DoubleAt(idx), true
	case valueabi.TagInt64:
		return float64(v.desc.Int64At(idx)), true
	case valueabi.TagUint64:
		return float64(v.desc.Uint64At(idx)), true
	case valueabi.TagNumRaw:
		f, err := strconv.ParseFloat(string(v.desc.NumRawAt(idx)), 64)
		return f, err == nil
	}
	return 0, false
}

// Bool returns the boolean held by v.
func (v *Value) Bool() (bool, bool) {
	if !v.hasTape() {
		return false, false
	}
	switch v.desc.TagAt(int(v.desc.Tidx)) {
	case valueabi.TagTrue:
		return true, true
	case valueabi.TagFalse:
		return false, true
	}
	return false, false
}

// IsNull reports whether v holds JSON null.
func (v *Value) IsNull() bool {
	return v.hasTape() && v.desc.TagAt(int(v.desc.Tidx)) == valueabi.TagNull
}

// Get walks an object path and returns its terminal value.
func (v *Value) Get(keys ...string) Value {
	cur := *v
	for _, key := range keys {
		if !cur.hasTape() {
			return Value{}
		}
		next, ok := cur.field(key)
		if !ok {
			return Value{}
		}
		cur = next
	}
	return cur
}

func (v *Value) child(idx int) Value {
	desc := v.desc
	desc.Tidx = int32(idx)
	return Value{desc: desc}
}

func (v *Value) field(key string) (Value, bool) {
	objIdx := int(v.desc.Tidx)
	if v.desc.TagAt(objIdx) != valueabi.TagObjBeg {
		return Value{}, false
	}
	end := v.desc.ContainerEnd(objIdx)
	cur := v.desc.SkipSeams(objIdx + 1)
	for cur < end {
		if string(v.desc.ScalarStringAt(cur)) == key {
			return v.child(cur + 1), true
		}
		cur = v.desc.Skip(cur + 1)
	}
	return Value{}, false
}

// Index returns the indexed array element. Negative indices count from the end.
func (v *Value) Index(i int) Value {
	if !v.hasTape() {
		return Value{}
	}
	if i < 0 {
		i += v.Len()
		if i < 0 {
			return Value{}
		}
	}
	root := int(v.desc.Tidx)
	if v.desc.TagAt(root) != valueabi.TagArrBeg {
		return Value{}
	}
	end := v.desc.ContainerEnd(root)
	cur := v.desc.SkipSeams(root + 1)
	for index := 0; cur < end; index++ {
		if index == i {
			return v.child(cur)
		}
		cur = v.desc.Skip(cur)
	}
	return Value{}
}

// ForEachKey visits object members in document order.
func (v *Value) ForEachKey(fn func(key string, val Value) bool) {
	if !v.hasTape() {
		return
	}
	root := int(v.desc.Tidx)
	if v.desc.TagAt(root) != valueabi.TagObjBeg {
		return
	}
	end := v.desc.ContainerEnd(root)
	cur := v.desc.SkipSeams(root + 1)
	for cur < end {
		key := string(v.desc.ScalarStringAt(cur))
		valIdx := cur + 1
		if !fn(key, v.child(valIdx)) {
			return
		}
		cur = v.desc.Skip(valIdx)
	}
}

// ForEachElem visits array elements in order.
func (v *Value) ForEachElem(fn func(i int, val Value) bool) {
	if !v.hasTape() {
		return
	}
	root := int(v.desc.Tidx)
	if v.desc.TagAt(root) != valueabi.TagArrBeg {
		return
	}
	end := v.desc.ContainerEnd(root)
	cur := v.desc.SkipSeams(root + 1)
	for index := 0; cur < end; index++ {
		if !fn(index, v.child(cur)) {
			return
		}
		cur = v.desc.Skip(cur)
	}
}

// GetString walks the key path and returns its string value.
func (v *Value) GetString(keys ...string) (string, bool) {
	cur := v.Get(keys...)
	return cur.Str()
}

// GetInt walks the key path and returns its integer value.
func (v *Value) GetInt(keys ...string) (int64, bool) {
	cur := v.Get(keys...)
	return cur.Int()
}

// GetFloat walks the key path and returns its numeric value.
func (v *Value) GetFloat(keys ...string) (float64, bool) {
	cur := v.Get(keys...)
	return cur.Float()
}

// GetBool walks the key path and returns its boolean value.
func (v *Value) GetBool(keys ...string) (bool, bool) {
	cur := v.Get(keys...)
	return cur.Bool()
}

// String returns the JSON representation of v.
func (v Value) String() string {
	if !v.hasTape() {
		return ""
	}
	return string(tapeToJSON(&v))
}
