package bind

import (
	"reflect"
	"testing"

	"github.com/velox-io/json/value"
	"github.com/velox-io/json/vbind"
)

// TypeTree aggregate flags are set as byproducts of vbind.Build. This
// recomputes each one the independent way, by
// scanning the finished Types/TypeMeta, and requires agreement.
//
// Worth pinning because the cheap derivation and the obvious one are different
// code: HasValueField rides the typ.KindValue arm, HasSplitTape rides the struct
// branch's metadata stamping. A future kind that reaches value.Value by another
// route, or a second place that stamps ReserveUnknownFieldOff, would set one and
// not the other. Getting HasSplitTape wrong undersizes the tape arena, which C
// writes into with no bounds check (see unmarshalPadded).
func TestPIN_TypeTreeFlagsMatchIndependentScan(t *testing.T) {
	type plain struct {
		A int    `json:"a"`
		B string `json:"b"`
	}
	type withValue struct {
		A int         `json:"a"`
		V value.Value `json:"v"`
	}
	type quotedString struct {
		S *string `json:"s,string"`
	}
	type quotedNumber struct {
		N int `json:"n,string"`
	}
	type sink struct {
		A    int         `json:"a"`
		Rest value.Value `json:",embed"`
	}
	type nestedValue struct {
		Inner []withValue `json:"inner"`
	}
	type dual struct {
		Kind string      `json:"kind"`
		Case any         `json:",embed" vjson:"variant=kind"`
		Rest value.Value `json:",embed"`
	}
	type dualCase struct {
		Name string `json:"name"`
	}
	vbind.DefineVariantCases[dual, struct {
		_ dualCase `case:"c1"`
	}]()

	for _, rt := range []reflect.Type{
		reflect.TypeFor[plain](),
		reflect.TypeFor[withValue](),
		reflect.TypeFor[quotedString](),
		reflect.TypeFor[quotedNumber](),
		reflect.TypeFor[sink](),
		reflect.TypeFor[nestedValue](),
		reflect.TypeFor[dual](),
		reflect.TypeFor[map[string]withValue](),
		reflect.TypeFor[[]sink](),
		reflect.TypeFor[any](),
	} {
		tt, err := vbind.TypeTreeOf(rt)
		if err != nil {
			t.Fatalf("%v: %v", rt, err)
		}

		wantValue := false
		for i := range tt.Types {
			if tt.Types[i].Kind == vbind.KindValue {
				wantValue = true
				break
			}
		}
		wantPoly := len(tt.Variants) > 0 || len(tt.Kindofs) > 0
		wantAppendStrings := false
		for i := range tt.Fields {
			f := &tt.Fields[i]
			if f.Flags&uint32(vbind.TagVDisc) != 0 {
				wantAppendStrings = true
				break
			}
			if f.Flags&uint32(vbind.TagQuoted) == 0 {
				continue
			}
			typeIdx := f.FieldTypeIndex(&tt.Types[0])
			for tt.Types[typeIdx].Kind == vbind.KindPointer {
				typeIdx = tt.Types[typeIdx].ChildIndex(&tt.Types[0])
			}
			if tt.Types[typeIdx].Kind == vbind.KindString {
				wantAppendStrings = true
				break
			}
		}
		wantSplit := false
		for i := range tt.Types {
			if tt.Types[i].Kind != vbind.KindStruct {
				continue
			}
			sm := tt.TypeMeta[i].StructMeta()
			if sm.ReserveUnknownFieldOff != 0xFFFFFFFF && sm.InlineVariantIdx != 0xFFFF {
				wantSplit = true
				break
			}
		}

		if tt.HasValueField != wantValue {
			t.Errorf("%v: HasValueField=%v scan=%v", rt, tt.HasValueField, wantValue)
		}
		if tt.HasPolyField != wantPoly {
			t.Errorf("%v: HasPolyField=%v scan=%v", rt, tt.HasPolyField, wantPoly)
		}
		if tt.HasSplitTape != wantSplit {
			t.Errorf("%v: HasSplitTape=%v scan=%v", rt, tt.HasSplitTape, wantSplit)
		}
		if tt.TapeBindMayAppendStrings != wantAppendStrings {
			t.Errorf("%v: TapeBindMayAppendStrings=%v scan=%v", rt, tt.TapeBindMayAppendStrings, wantAppendStrings)
		}
		t.Logf("%-28v value=%-5v poly=%-5v split=%v appendStrings=%v", rt, tt.HasValueField,
			tt.HasPolyField, tt.HasSplitTape, tt.TapeBindMayAppendStrings)
	}
}
