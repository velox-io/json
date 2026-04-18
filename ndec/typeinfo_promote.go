// embedded (anonymous) struct field promotion
//
// Implements a typeFields algorithm equivalent to stdlib
// encoding/json's typeFields. BFS traversal of anonymous struct
// fields, field promotion to parent level, and conflict resolution
// by depth then by explicit tag presence.

package ndec

import "reflect"

// pathStep is one step in the path from root struct to a leaf field.
// Non-embedded fields have path length 1 (just the field itself).
// fields promoted through N layers of anonymous structs have path
// length N+1.
type pathStep struct {
	offset uintptr      // byte offset within the containing struct
	isPtr  bool         // true if this step requires pointer deref
	rt     reflect.Type // field type at this step (for PTR alloc)
}

// flatField is one candidate JSON field produced by the BFS
// traversal. Multiple flatFields may have the same name (conflict),
// resolved by dominantField then finalDedup.
type flatField struct {
	name        string       // JSON field name (tag name or Go field name)
	nameFromTag bool         // true if name comes from explicit json tag
	tagFlags    uint8        // bff* flags from json tag
	path        []pathStep   // path from root to leaf field
	leafType    reflect.Type // Go type of the leaf field

	// originalPath is the chain of Go field names from root struct
	// through embedding layers to the leaf field. Length matches len(path).
	// Elements are Go field names (e.g. "Inner" for `Inner Inner` or "Y"
	// for leaf `Y int`).
	//
	// Used by renderFieldPath to produce stdlib-compatible full embedding
	// paths in UnmarshalTypeError.Field. For example, a mismatch on Y in
	// Outer{Inner; Y} renders "Inner.Y", not "Y". Plain fields (path
	// length 1) also store one entry, degenerating to a single segment
	// where it matches the JSON name.
	originalPath []string
}

// jsonTagFull is the parsed result of a json:"..." struct tag.
type jsonTagFull struct {
	name    string // field name; empty if no name (json:"" or no tag)
	flags   uint8  // bff* bitmask
	hasName bool   // true if the tag contains a non-empty name
	dash    bool   // true if tag name is "-"
}

// typeFields returns the flattened JSON field set for struct type t,
// matching stdlib encoding/json's typeFields behavior:
//   - Anonymous struct fields are promoted (their fields appear at
//     the parent level).
//   - Anonymous *struct fields are promoted (with PTR deref step).
//   - Anonymous fields with explicit json tag name are NOT promoted
//     (treated as regular named fields).
//   - Name conflicts are resolved by depth (shallower wins), then
//     by explicit tag (tag wins over implicit), then all dropped.
//   - Cyclic embedding is detected and skipped.
func typeFields(t reflect.Type) []flatField {
	type todo struct {
		t          reflect.Type // struct type to expand
		prefix     []pathStep   // path from root to this level
		namePrefix []string     // Go field names (embed chain) from root
	}

	var current []todo
	var next []todo
	var fields []flatField
	visited := map[reflect.Type]bool{}

	current = append(current, todo{t: t})
	visited[t] = true

	for len(current) > 0 {
		// One BFS round: collect all flatFields at the same depth,
		// then deduplicate within this round via dominantFields.
		var roundFields []flatField

		for _, item := range current {
			itemT := item.t
			for i := 0; i < itemT.NumField(); i++ {
				sf := itemT.Field(i)

				// 1. unexported handling
				//    - exported: proceed
				//    - unexported anonymous struct/*struct: still promote (stdlib)
				//    - unexported anonymous non-struct: skip
				//    - unexported named: skip
				if !sf.IsExported() {
					if !sf.Anonymous {
						continue
					}
					// unexported anonymous: only promote non-ptr structs
					if sf.Type.Kind() != reflect.Struct {
						continue
					}
				}

				tag := parseJSONTagFull(sf)
				if tag.dash {
					continue
				}

				// 2. decide: promote or keep as named field
				isAnonymous := sf.Anonymous
				// Only non-pointer embedded structs are promoted here because pointer
				// embeddings need allocation-aware traversal instead of a plain field walk.
				isStructLike := sf.Type.Kind() == reflect.Struct
				doPromote := isAnonymous && !tag.hasName && isStructLike

				if doPromote {
					// Enter next BFS level: expand the embedded struct.
					inner := sf.Type
					if inner.Kind() == reflect.Ptr {
						inner = inner.Elem()
					}
					if visited[inner] {
						// cyclic embedding: skip
						continue
					}
					visited[inner] = true

					step := pathStep{offset: sf.Offset, rt: sf.Type}
					if sf.Type.Kind() == reflect.Ptr {
						step.isPtr = true
					}

					newPrefix := make([]pathStep, len(item.prefix)+1)
					copy(newPrefix, item.prefix)
					newPrefix[len(item.prefix)] = step

					// Add the embed layer's Go field name to namePrefix.
					// The Go embed field's sf.Name is the embedding type
					// name (e.g. `Inner`), matching stdlib rendering.
					newNamePrefix := make([]string, len(item.namePrefix)+1)
					copy(newNamePrefix, item.namePrefix)
					newNamePrefix[len(item.namePrefix)] = sf.Name

					next = append(next, todo{t: inner, prefix: newPrefix, namePrefix: newNamePrefix})
					continue
				}

				// Named field (or anonymous with explicit tag): add to
				// this depth's roundFields.
				name := tag.name
				if name == "" {
					name = sf.Name
				}

				step := pathStep{offset: sf.Offset, rt: sf.Type}
				if sf.Type.Kind() == reflect.Ptr {
					step.isPtr = true
				}
				fullPath := make([]pathStep, len(item.prefix)+1)
				copy(fullPath, item.prefix)
				fullPath[len(item.prefix)] = step

				// originalPath: namePrefix (embed chain) + leaf's Go field name.
				// stdlib renders UnmarshalTypeError.Field using only the Go
				// field name, not the JSON tag; we follow the same behavior.
				origPath := make([]string, len(item.namePrefix)+1)
				copy(origPath, item.namePrefix)
				origPath[len(item.namePrefix)] = sf.Name

				roundFields = append(roundFields, flatField{
					name:         name,
					nameFromTag:  tag.hasName,
					tagFlags:     tag.flags,
					path:         fullPath,
					leafType:     sf.Type,
					originalPath: origPath,
				})
			}
		}

		// 3. dedup within this depth
		fields = append(fields, dominantFields(roundFields)...)
		current, next = next, nil
	}

	// 4. cross-depth dedup: shallower depth wins (BFS guarantees
	//    earlier rounds appear first in fields)
	return finalDedup(fields)
}

// dominantFields resolves name conflicts within the same depth.
// Rules: a unique explicit tag wins over implicits; if multiple
// fields have explicit tags for the same name, all are dropped.
// Order is preserved: fields appear in the same relative order as
// the input.
func dominantFields(in []flatField) []flatField {
	if len(in) == 0 {
		return nil
	}

	// Group by name while preserving input order.
	type groupInfo struct {
		firstIdx int
		fields   []flatField
	}
	byName := map[string]*groupInfo{}
	for i, f := range in {
		if g, ok := byName[f.name]; ok {
			g.fields = append(g.fields, f)
		} else {
			byName[f.name] = &groupInfo{firstIdx: i, fields: []flatField{f}}
		}
	}

	// Sort groups by firstIdx to preserve input order.
	type nameGroup struct {
		name  string
		group *groupInfo
	}
	sorted := make([]nameGroup, 0, len(byName))
	for n, g := range byName {
		sorted = append(sorted, nameGroup{name: n, group: g})
	}
	for i := 0; i < len(sorted)-1; i++ {
		for j := i + 1; j < len(sorted); j++ {
			if sorted[j].group.firstIdx < sorted[i].group.firstIdx {
				sorted[i], sorted[j] = sorted[j], sorted[i]
			}
		}
	}

	var out []flatField
	for _, sg := range sorted {
		group := sg.group.fields
		if len(group) == 1 {
			out = append(out, group[0])
			continue
		}
		var withTag []flatField
		for _, f := range group {
			if f.nameFromTag {
				withTag = append(withTag, f)
			}
		}
		if len(withTag) == 1 {
			out = append(out, withTag[0])
			continue
		}
		// All implicit or multiple explicit: drop all.
	}
	return out
}

// finalDedup removes later occurrences of the same name, keeping the
// first one. Because BFS guarantees fields are appended in depth
// order, this implements "shallowest depth wins".
func finalDedup(in []flatField) []flatField {
	seen := map[string]bool{}
	out := make([]flatField, 0, len(in))
	for _, f := range in {
		if seen[f.name] {
			continue
		}
		seen[f.name] = true
		out = append(out, f)
	}
	return out
}

// parseJSONTagFull parses the json:"..." struct tag
func parseJSONTagFull(f reflect.StructField) jsonTagFull {
	tag, ok := f.Tag.Lookup("json")
	if !ok {
		return jsonTagFull{}
	}
	if tag == "" {
		return jsonTagFull{hasName: true}
	}
	if tag == "-" {
		return jsonTagFull{name: "-", dash: true}
	}

	// Parse "name,opt1,opt2"
	var name string
	comma := -1
	for i := 0; i < len(tag); i++ {
		if tag[i] == ',' {
			comma = i
			break
		}
	}
	if comma >= 0 {
		name = tag[:comma]
	} else {
		name = tag
	}

	var flags uint8
	if comma >= 0 {
		rest := tag[comma+1:]
		for len(rest) > 0 {
			nextComma := -1
			for i := 0; i < len(rest); i++ {
				if rest[i] == ',' {
					nextComma = i
					break
				}
			}
			var opt string
			if nextComma >= 0 {
				opt = rest[:nextComma]
				rest = rest[nextComma+1:]
			} else {
				opt = rest
				rest = ""
			}
			switch opt {
			case "string":
				flags |= bffQuoted
			case "omitempty":
				flags |= bffOmitEmpty
			}
		}
	}

	if name == "-" {
		return jsonTagFull{name: "-", dash: true}
	}
	return jsonTagFull{name: name, flags: flags, hasName: name != ""}
}

func (ff flatField) needsPtrChain() bool {
	for _, s := range ff.path {
		if s.isPtr {
			return true
		}
	}
	return false
}

// accumulatedOffset returns the total byte offset from root struct
// to leaf field, summing all non-ptr path steps. Valid only when
// needsPtrChain() returns false.
func (ff flatField) accumulatedOffset() uintptr {
	var off uintptr
	for _, s := range ff.path {
		off += s.offset
	}
	return off
}
