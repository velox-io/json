package vjson

import (
	stdjson "encoding/json"
	"testing"
)

// This file tests that struct field names (JSON keys) in marshal output are
// correct, especially when structs flow through any/interface{} fields which
// trigger on-the-fly Blueprint compilation and key pool insertion.
//
// All tests compare vjson output byte-for-byte with encoding/json to catch
// key corruption bugs (e.g. stale KeyPoolBase after interface cache miss).

// Test types (unexported, local to this file)

type fnAddress struct {
	City   string `json:"city"`
	Street string `json:"street"`
	Detail any    `json:"detail,omitempty"`
}

type fnGeoLocation struct {
	Lat       float64  `json:"lat"`
	Lng       float64  `json:"lng"`
	Altitude  float64  `json:"altitude"`
	Accuracy  float64  `json:"accuracy"`
	Provider  string   `json:"provider"`
	Country   string   `json:"country"`
	Province  string   `json:"province"`
	District  string   `json:"district"`
	ZipCode   string   `json:"zip_code"`
	Timezone  string   `json:"timezone"`
	Timestamp int64    `json:"timestamp"`
	Tags      []string `json:"tags"`
}

type fnProfile struct {
	Bio    string `json:"bio"`
	Avatar string `json:"avatar_url"`
	Age    int    `json:"age"`
}

// Tests

// TestMarshal_StructFieldNames_StdlibCompat verifies that basic struct field
// names with json tags produce output identical to encoding/json.
func TestMarshal_StructFieldNames_StdlibCompat(t *testing.T) {
	type S struct {
		FirstName string  `json:"first_name"`
		LastName  string  `json:"last_name"`
		Age       int     `json:"age"`
		Score     float64 `json:"score"`
		Active    bool    `json:"active"`
		NoTag     string  // exported, no tag → key is "NoTag"
	}

	v := S{
		FirstName: "Alice",
		LastName:  "Smith",
		Age:       30,
		Score:     95.5,
		Active:    true,
		NoTag:     "visible",
	}

	got, err := Marshal(&v)
	if err != nil {
		t.Fatalf("Marshal failed: %v", err)
	}
	want, _ := stdjson.Marshal(&v)
	if string(got) != string(want) {
		t.Errorf("field name mismatch\n got: %s\nwant: %s", got, want)
	}
}

// TestMarshal_StructInAny_StdlibCompat verifies that a struct stored in an
// any field has its field names (JSON keys) encoded correctly. This is the
// core scenario for the key pool stale pointer bug.
func TestMarshal_StructInAny_StdlibCompat(t *testing.T) {
	type Outer struct {
		Name  string `json:"name"`
		Extra any    `json:"extra"`
	}

	v := Outer{
		Name: "test",
		Extra: fnProfile{
			Bio:    "developer",
			Avatar: "https://example.com/avatar.png",
			Age:    28,
		},
	}

	got, err := Marshal(&v)
	if err != nil {
		t.Fatalf("Marshal failed: %v", err)
	}
	want, _ := stdjson.Marshal(&v)
	if string(got) != string(want) {
		t.Errorf("struct-in-any field name mismatch\n got: %s\nwant: %s", got, want)
	}
}

// TestMarshal_NestedAnyChain_StdlibCompat tests multi-level any nesting:
// Outer.Extra(any) → fnAddress → fnAddress.Detail(any) → fnGeoLocation.
// This reproduces the exact bug pattern from examples/marshal/.
func TestMarshal_NestedAnyChain_StdlibCompat(t *testing.T) {
	type Outer struct {
		ID    int    `json:"id"`
		Name  string `json:"name"`
		Extra any    `json:"extra"`
	}

	v := Outer{
		ID:   1,
		Name: "alice",
		Extra: fnAddress{
			City:   "Shenzhen",
			Street: "Keyuan Rd",
			Detail: fnGeoLocation{
				Lat: 22.5431, Lng: 114.0579, Altitude: 15.3, Accuracy: 10.0,
				Provider: "gps", Country: "China", Province: "Guangdong",
				District: "Nanshan", ZipCode: "518057", Timezone: "Asia/Shanghai",
				Timestamp: 1709472000,
				Tags:      []string{"office", "primary", "verified"},
			},
		},
	}

	got, err := Marshal(&v)
	if err != nil {
		t.Fatalf("Marshal failed: %v", err)
	}
	want, _ := stdjson.Marshal(&v)
	if string(got) != string(want) {
		t.Errorf("nested any chain field name mismatch\n got: %s\nwant: %s", got, want)
	}
}

// TestMarshal_MultipleDistinctTypesInAny_StdlibCompat tests that multiple
// any fields holding different struct types all get correct field names.
// Each new type triggers a separate on-the-fly Blueprint compilation and
// key pool insertion.
func TestMarshal_MultipleDistinctTypesInAny_StdlibCompat(t *testing.T) {
	type Outer struct {
		Name   string `json:"name"`
		Field1 any    `json:"field1"`
		Field2 any    `json:"field2"`
		Field3 any    `json:"field3"`
	}

	v := Outer{
		Name: "multi",
		Field1: fnProfile{
			Bio:    "engineer",
			Avatar: "img.png",
			Age:    25,
		},
		Field2: fnAddress{
			City:   "Beijing",
			Street: "Chang'an Ave",
		},
		Field3: fnGeoLocation{
			Lat: 39.9042, Lng: 116.4074,
			Provider: "network", Country: "China", Province: "Beijing",
			District: "Dongcheng", ZipCode: "100010", Timezone: "Asia/Shanghai",
			Timestamp: 1709472000,
		},
	}

	got, err := Marshal(&v)
	if err != nil {
		t.Fatalf("Marshal failed: %v", err)
	}
	want, _ := stdjson.Marshal(&v)
	if string(got) != string(want) {
		t.Errorf("multiple types in any field name mismatch\n got: %s\nwant: %s", got, want)
	}
}

// TestMarshalIndent_StructInAny_StdlibCompat tests the indent (full) encvm
// variant with struct-in-any nesting, ensuring field names are correct in
// indented output as well.
func TestMarshalIndent_StructInAny_StdlibCompat(t *testing.T) {
	type Outer struct {
		ID    int    `json:"id"`
		Name  string `json:"name"`
		Extra any    `json:"extra"`
	}

	v := Outer{
		ID:   42,
		Name: "indent-test",
		Extra: fnAddress{
			City:   "Shanghai",
			Street: "Nanjing Rd",
			Detail: fnGeoLocation{
				Lat: 31.2304, Lng: 121.4737, Altitude: 4.0, Accuracy: 5.0,
				Provider: "gps", Country: "China", Province: "Shanghai",
				District: "Huangpu", ZipCode: "200001", Timezone: "Asia/Shanghai",
				Timestamp: 1709472000,
				Tags:      []string{"hq", "main"},
			},
		},
	}

	got, err := MarshalIndent(&v, "", "  ")
	if err != nil {
		t.Fatalf("MarshalIndent failed: %v", err)
	}
	want, _ := stdjson.MarshalIndent(&v, "", "  ")
	if string(got) != string(want) {
		t.Errorf("indent struct-in-any field name mismatch\n got: %s\nwant: %s", got, want)
	}
}
