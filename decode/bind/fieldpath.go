package bind

import (
	"strconv"

	"github.com/velox-io/json/internal/gdec"
	"github.com/velox-io/json/vbind"
)

// The type mismatch error carries the identity of the offending site: the
// member path the value sat on and the leaf Go type that rejected it. Both
// engines record only the token's source offset, so the identity is rebuilt
// after the fact rather than carried through the hot bind. A walk over the
// source reconstructs the JSON pointer to that offset, and the TypeTree then
// resolves the pointer to the leaf type and the field's `,string` flag.
//
// The rebuilt identity matches the UnmarshalTypeError encoding/json reports
// today, which the json v2 implementation serving its v1 API defines: Struct
// names the root type, Field is the JSON pointer with the separators turned
// into dots, Type is the leaf destination, Offset is one past the offending
// token (one past the opening bracket for containers), and Value names the
// value's kind. Everything is computed off the error path only.

// mismatchContext is the rebuilt identity of one BindErrTypeMismatch site.
type mismatchContext struct {
	// tokens is the JSON pointer to the offending value: object member names
	// and map keys as written, array indices in decimal. Empty at the root.
	tokens []string
	// tokenEnd is the source offset one past the offending token, in the
	// caller's document coordinates.
	tokenEnd uint64
	// leaf is the TypeTree index of the destination that rejected the value.
	leaf uint32
	// quoted reports that the field binding the value carries `,string`.
	quoted bool
	// strRaw holds the offending token's raw bytes when it is a JSON string,
	// including its quotes, and is empty otherwise.
	strRaw string
	// strBody is strRaw unescaped.
	strBody string
}

// mismatchWalkDepth bounds the pointer rebuild. The engines themselves accept
// 255 nested containers, so a bound mismatch site never sits deeper; the slack
// only guards the walk against unrelated malformed input.
const mismatchWalkDepth = 300

// rebuildMismatchContext rebuilds the identity of the mismatch at document
// offset pos, reading src as the window whose first byte is at srcBase. The
// second result is false when the window no longer holds the bytes, the
// offset is not a value start, or the TypeTree cannot resolve the pointer.
// Callers fall back to the position-only error in that case.
func (p *Parser) rebuildMismatchContext(src []byte, srcBase, pos uint64) (mismatchContext, bool) {
	if pos < srcBase {
		return mismatchContext{}, false
	}
	local := int(pos - srcBase)
	if local >= len(src) {
		return mismatchContext{}, false
	}
	tokens, end, ok := walkJSONPath(src, local)
	if !ok {
		return mismatchContext{}, false
	}
	leaf, quoted, ok := resolveMismatchPath(p.tt, tokens)
	if !ok {
		return mismatchContext{}, false
	}
	mc := mismatchContext{
		tokens:   tokens,
		tokenEnd: srcBase + uint64(end),
		leaf:     leaf,
		quoted:   quoted,
	}
	if src[local] == '"' {
		mc.strRaw = string(src[local:end])
		if body, _, ok := gdec.AppendString(nil, src, local+1); ok {
			mc.strBody = string(body)
		} else {
			mc.strBody = mc.strRaw
		}
	}
	return mc, true
}

// mismatchValueName refines the value description a `,string` binding
// reports. A quoted string that failed strconv on a numeric destination is
// described as the number it was meant to be, and on a bool destination as
// the string it is, both carrying the literal itself. Every other shape
// keeps the category the byte at the offset names. The bool form also
// reports the strconv failure, which the numeric form suppresses to match
// the stdlib wording.
func mismatchValueName(category string, mc mismatchContext, leaf *vbind.BindType) (string, error) {
	if !mc.quoted || mc.strRaw == "" {
		return category, nil
	}
	switch k := leaf.Kind; {
	case k == vbind.KindNumber ||
		(k >= vbind.KindInt && k <= vbind.KindFloat64):
		return "number " + mc.strBody, nil
	case k == vbind.KindBool:
		return "string " + mc.strRaw, strconv.ErrSyntax
	}
	return category, nil
}

// walkJSONPath walks src's first JSON value and reports the JSON pointer to
// the value starting exactly at target. ok is false when target is not a
// value start, the input before it is malformed, or nesting exceeds
// mismatchWalkDepth. The end result is one past the target token, matching
// the offset encoding/json reports for it.
func walkJSONPath(src []byte, target int) (tokens []string, end int, ok bool) {
	w := pathWalk{src: src, target: target}
	after := w.value(gdec.SkipWS(src, 0), 0)
	if !w.found || after < 0 {
		return nil, 0, false
	}
	return w.tokens, w.end, true
}

// pathWalk tracks the pointer of the value being walked. tokens holds the
// names from the document root down to the value currently being entered.
type pathWalk struct {
	src    []byte
	target int
	tokens []string
	end    int
	found  bool
}

// value walks the value at token start i and returns the index past it, or
// minus one when the input is malformed at this level. Entering the target
// records the pointer built so far and the token end.
func (w *pathWalk) value(i, depth int) int {
	src := w.src
	if i >= len(src) {
		return -1
	}
	if i == w.target {
		end, ok := gdec.TokenEnd(src, i)
		if !ok {
			return -1
		}
		w.found = true
		w.end = end
		return end
	}
	switch src[i] {
	case '{':
		return w.object(i, depth)
	case '[':
		return w.array(i, depth)
	default:
		end, ok := gdec.TokenEnd(src, i)
		if !ok {
			return -1
		}
		return end
	}
}

// object walks the object whose opening brace is at i.
func (w *pathWalk) object(i, depth int) int {
	if depth >= mismatchWalkDepth {
		return -1
	}
	src := w.src
	i = gdec.SkipWS(src, i+1)
	if i >= len(src) {
		return -1
	}
	if src[i] == '}' {
		return i + 1
	}
	for {
		if src[i] != '"' {
			return -1
		}
		name, next, ok := pathMemberName(src, i)
		if !ok {
			return -1
		}
		i = gdec.SkipWS(src, next)
		if i >= len(src) || src[i] != ':' {
			return -1
		}
		w.tokens = append(w.tokens, name)
		after := w.value(gdec.SkipWS(src, i+1), depth+1)
		if w.found {
			return after
		}
		w.tokens = w.tokens[:len(w.tokens)-1]
		if after < 0 {
			return -1
		}
		i = gdec.SkipWS(src, after)
		if i >= len(src) {
			return -1
		}
		if src[i] == '}' {
			return i + 1
		}
		if src[i] != ',' {
			return -1
		}
		i = gdec.SkipWS(src, i+1)
		if i >= len(src) {
			return -1
		}
	}
}

// array walks the array whose opening bracket is at i.
func (w *pathWalk) array(i, depth int) int {
	if depth >= mismatchWalkDepth {
		return -1
	}
	src := w.src
	i = gdec.SkipWS(src, i+1)
	if i >= len(src) {
		return -1
	}
	if src[i] == ']' {
		return i + 1
	}
	for idx := 0; ; idx++ {
		w.tokens = append(w.tokens, strconv.Itoa(idx))
		after := w.value(i, depth+1)
		if w.found {
			return after
		}
		w.tokens = w.tokens[:len(w.tokens)-1]
		if after < 0 {
			return -1
		}
		i = gdec.SkipWS(src, after)
		if i >= len(src) {
			return -1
		}
		if src[i] == ']' {
			return i + 1
		}
		if src[i] != ',' {
			return -1
		}
		i = gdec.SkipWS(src, i+1)
		if i >= len(src) {
			return -1
		}
	}
}

// pathMemberName reads the member name whose opening quote is at i and
// returns it unescaped with the index past the closing quote.
func pathMemberName(src []byte, i int) (string, int, bool) {
	name, end, ok := gdec.AppendString(nil, src, i+1)
	if !ok {
		return "", 0, false
	}
	return string(name), end + 1, true
}

// resolveMismatchPath walks tt from its root along the pointer tokens and
// returns the leaf destination type with the `,string` flag of the field the
// path ends at. Pointer layers add no token, so the walk passes through
// them. The second result is false when the pointer names no field of the
// tree or crosses a runtime-selected shape, an interface or value.Value,
// whose leaf type only the input picks.
func resolveMismatchPath(tt *vbind.TypeTree, tokens []string) (uint32, bool, bool) {
	idx := tt.Root
	types, fields := &tt.Types[0], &tt.Fields[0]
	quoted := false
	for i := 0; i < len(tokens); {
		switch tt.Types[idx].Kind {
		case vbind.KindPointer:
			idx = tt.Types[idx].ChildIndex(types)
		case vbind.KindStruct:
			first := tt.Types[idx].StructFirstFieldIndex(fields)
			count := tt.Types[idx].Struct().FieldCount
			var found *vbind.BindField
			for j := uint32(0); j < count; j++ {
				if tt.FieldNames[first+j] == tokens[i] {
					found = &tt.Fields[first+j]
					break
				}
			}
			if found == nil {
				return 0, false, false
			}
			quoted = found.Flags&uint32(vbind.TagQuoted) != 0
			idx = found.FieldTypeIndex(types)
			i++
		case vbind.KindSlice, vbind.KindArray, vbind.KindStream, vbind.KindMap:
			idx = tt.Types[idx].ChildIndex(types)
			i++
		default:
			return 0, false, false
		}
	}
	for tt.Types[idx].Kind == vbind.KindPointer {
		idx = tt.Types[idx].ChildIndex(types)
	}
	return idx, quoted, true
}
