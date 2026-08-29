package vcopy

import (
	"github.com/velox-io/json/typ"
)

// isAcyclic reports whether values of type ut can participate in a reference
// cycle. A type is acyclic if, starting from a value of that type, no path
// of pointer/interface dereferences leads back to a value of the same type
// (or to itself via a longer cycle).
//
// The result is cached on the Copier for the lifetime of the Copier. Most
// business types are acyclic; the first encounter pays a one-time graph
// walk, subsequent encounters are a map lookup.
func (c *Copier) isAcyclic(ut *typ.UniType) bool {
	if c.acyclicCache == nil {
		c.acyclicCache = make(map[*typ.UniType]acyclicState, 16)
	}
	switch c.acyclicCache[ut] {
	case acyclicTrue:
		return true
	case acyclicFalse:
		return false
	}
	// Compute and cache. A fresh acyclicState (acyclicUnknown) means
	// "currently being analyzed": if we re-enter this type during the
	// walk, we've found a cycle.
	r := analyzeAcyclic(ut, c.acyclicCache)
	if r {
		c.acyclicCache[ut] = acyclicTrue
	} else {
		c.acyclicCache[ut] = acyclicFalse
	}
	return r
}

// analyzeAcyclic walks the type graph reachable from ut and reports whether
// it is acyclic. The path map holds types currently on the DFS stack; a
// repeat visit while a type is on the stack indicates a cycle.
//
// Already-settled entries (acyclicTrue/acyclicFalse) short-circuit.
// acyclicUnknown is the on-stack sentinel: re-encountering it during the
// same DFS means a back edge → not acyclic.
func analyzeAcyclic(ut *typ.UniType, settled map[*typ.UniType]acyclicState) bool {
	switch settled[ut] {
	case acyclicTrue:
		return true
	case acyclicFalse:
		return false
	case acyclicUnknown:
		// On-stack: back edge → cycle.
		return false
	}
	// Mark as on-stack for the duration of this DFS branch.
	settled[ut] = acyclicUnknown

	ok := true
	switch ut.Kind {
	case typ.KindBool,
		typ.KindInt, typ.KindInt8, typ.KindInt16, typ.KindInt32, typ.KindInt64,
		typ.KindUint, typ.KindUint8, typ.KindUint16, typ.KindUint32, typ.KindUint64,
		typ.KindFloat32, typ.KindFloat64,
		typ.KindString,
		typ.KindRawMessage, typ.KindValue, typ.KindNumber:
		// Leaves: no outgoing reference edges.
	case typ.KindStruct:
		si := ut.Ext.(*typ.StructTypeInfo)
		for i := range si.Fields {
			if !analyzeAcyclic(si.Fields[i].FieldType, settled) {
				ok = false
				break
			}
		}
	case typ.KindSlice:
		si := ut.Ext.(*typ.SliceTypeInfo)
		ok = analyzeAcyclic(si.ElemType, settled)
	case typ.KindArray:
		ai := ut.Ext.(*typ.ArrayTypeInfo)
		ok = analyzeAcyclic(ai.ElemType, settled)
	case typ.KindPointer:
		pi := ut.Ext.(*typ.PointerTypeInfo)
		ok = analyzeAcyclic(pi.ElemType, settled)
	case typ.KindMap:
		mi := ut.Ext.(*typ.MapTypeInfo)
		// Map keys are comparable (and thus acyclic-by-construction in
		// practice: strings, numbers, bools, arrays/structs of those).
		// Map values may cycle.
		ok = analyzeAcyclic(mi.ValType, settled)
	case typ.KindAny, typ.KindIface:
		// Dynamic type unknown at static-analysis time; conservatively
		// assume it may participate in a cycle. This forces the visiting
		// map on for any struct containing an interface field.
		ok = false
	default:
		// Unknown kind: be conservative.
		ok = false
	}

	if ok {
		settled[ut] = acyclicTrue
	} else {
		settled[ut] = acyclicFalse
	}
	return ok
}
