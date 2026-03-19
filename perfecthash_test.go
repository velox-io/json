package vjson

import (
	"fmt"
	"reflect"
	"testing"
)

// Test Helpers

// lookupField is a test helper that wraps LookupFieldBytes for string input.
func lookupField(dec *StructCodec, key string) *TypeInfo {
	return dec.LookupFieldBytes([]byte(key))
}

// makeTestStructCodec builds a StructCodec from a list of field names.
// This bypasses reflect to create controlled test scenarios.
func makeTestStructCodec(names []string) *StructCodec {
	dec := &StructCodec{
		Fields: make([]TypeInfo, len(names)),
	}
	for i, name := range names {
		dec.Fields[i] = TypeInfo{
			JSONName:      name,
			JSONNameLower: toLowerASCII(name),
			Offset:        uintptr(i * 8),
			Kind:          KindString,
		}
	}
	buildLookup(dec)
	return dec
}

// toLowerASCII Tests

func TestToLowerASCII_AllLower(t *testing.T) {
	s := "hello_world"
	result := toLowerASCII(s)
	if result != "hello_world" {
		t.Errorf("expected hello_world, got %s", result)
	}
}

func TestToLowerASCII_WithUpper(t *testing.T) {
	tests := []struct{ in, want string }{
		{"Hello", "hello"},
		{"ALLCAPS", "allcaps"},
		{"camelCase", "camelcase"},
		{"MixedCase123", "mixedcase123"},
		{"a", "a"},
		{"A", "a"},
		{"JSON", "json"},
		{"createdAt", "createdat"},
		{"Created_At", "created_at"},
	}
	for _, tt := range tests {
		got := toLowerASCII(tt.in)
		if got != tt.want {
			t.Errorf("toLowerASCII(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

func TestToLowerASCII_Empty(t *testing.T) {
	if got := toLowerASCII(""); got != "" {
		t.Errorf("expected empty, got %q", got)
	}
}

func TestToLowerASCII_NonASCII(t *testing.T) {
	// Non-ASCII bytes should be left unchanged
	s := "café"
	got := toLowerASCII(s)
	if got != "café" {
		t.Errorf("toLowerASCII(%q) = %q, want %q", s, got, "café")
	}
}

// Build Correctness Tests

func TestBuildLookup_Empty(t *testing.T) {
	dec := makeTestStructCodec(nil)
	if _, ok := dec.Lookup.(emptyLookup); !ok {
		t.Fatalf("expected emptyLookup, got %T", dec.Lookup)
	}
	if fi := lookupField(dec, "anything"); fi != nil {
		t.Error("expected nil for empty struct")
	}
}

func TestBuildLookup_SingleField(t *testing.T) {
	dec := makeTestStructCodec([]string{"id"})
	fi := lookupField(dec, "id")
	if fi == nil {
		t.Fatal("expected to find 'id'")
	}
	if fi.JSONName != "id" {
		t.Errorf("expected JSONName='id', got %q", fi.JSONName)
	}
}

func TestBuildLookup_LinearRange(t *testing.T) {
	// 1-4 fields should now use bitmap lookup
	for n := 1; n <= 4; n++ {
		names := make([]string, n)
		for i := range n {
			names[i] = fmt.Sprintf("field%d", i)
		}
		dec := makeTestStructCodec(names)

		if _, ok := dec.Lookup.(*bitmapLookup8); !ok {
			t.Errorf("n=%d: expected bitmapLookup8, got %T", n, dec.Lookup)
		}

		for _, name := range names {
			fi := lookupField(dec, name)
			if fi == nil {
				t.Errorf("n=%d: expected to find %q", n, name)
			} else if fi.JSONName != name {
				t.Errorf("n=%d: expected %q, got %q", n, name, fi.JSONName)
			}
		}

		// Unknown key
		if fi := lookupField(dec, "nonexistent"); fi != nil {
			t.Errorf("n=%d: expected nil for unknown key", n)
		}
	}
}

func TestBuildLookup_BitmapRange(t *testing.T) {
	// 5-8 fields should use bitmap lookup
	for n := 5; n <= 8; n++ {
		names := make([]string, n)
		for i := range n {
			names[i] = fmt.Sprintf("field_%d", i)
		}
		dec := makeTestStructCodec(names)

		if _, ok := dec.Lookup.(*bitmapLookup8); !ok {
			t.Errorf("n=%d: expected bitmapLookup8, got %T", n, dec.Lookup)
		}

		for _, name := range names {
			fi := lookupField(dec, name)
			if fi == nil {
				t.Errorf("n=%d: expected to find %q", n, name)
			} else if fi.JSONName != name {
				t.Errorf("n=%d: expected %q, got %q", n, name, fi.JSONName)
			}
		}

		if fi := lookupField(dec, "nonexistent"); fi != nil {
			t.Errorf("n=%d: expected nil for unknown key", n)
		}
	}
}

func TestBuildLookup_PerfectHashRange(t *testing.T) {
	// 9-32 fields should use perfect hash
	for _, n := range []int{9, 12, 16, 20, 24, 28, 32} {
		names := make([]string, n)
		for i := range n {
			names[i] = fmt.Sprintf("field_%d", i)
		}
		dec := makeTestStructCodec(names)

		switch dec.Lookup.(type) {
		case *perfectSimpleLookup, *perfectFNVLookup, *perfectMulaccLookup:
			// ok
		default:
			t.Errorf("n=%d: expected perfect hash lookup, got %T", n, dec.Lookup)
			continue
		}

		for _, name := range names {
			fi := lookupField(dec, name)
			if fi == nil {
				t.Errorf("n=%d: expected to find %q", n, name)
			} else if fi.JSONName != name {
				t.Errorf("n=%d: expected %q, got %q", n, name, fi.JSONName)
			}
		}

		if fi := lookupField(dec, "nonexistent"); fi != nil {
			t.Errorf("n=%d: expected nil for unknown key", n)
		}
	}
}

func TestBuildLookup_MapFallback(t *testing.T) {
	// 33+ fields should use map fallback
	n := 40
	names := make([]string, n)
	for i := range n {
		names[i] = fmt.Sprintf("field_%d", i)
	}
	dec := makeTestStructCodec(names)

	if _, ok := dec.Lookup.(*mapLookup); !ok {
		t.Fatalf("expected mapLookup for 40 fields, got %T", dec.Lookup)
	}

	for _, name := range names {
		fi := lookupField(dec, name)
		if fi == nil {
			t.Errorf("expected to find %q", name)
		}
	}
}

// Case-Insensitive Lookup Tests

func TestLookup_CaseInsensitive(t *testing.T) {
	dec := makeTestStructCodec([]string{"Name", "Age", "Email"})

	tests := []struct {
		key      string
		wantName string
	}{
		{"Name", "Name"},
		{"name", "Name"},
		{"NAME", "Name"},
		{"nAmE", "Name"},
		{"Age", "Age"},
		{"age", "Age"},
		{"AGE", "Age"},
		{"Email", "Email"},
		{"email", "Email"},
		{"EMAIL", "Email"},
	}

	for _, tt := range tests {
		fi := lookupField(dec, tt.key)
		if fi == nil {
			t.Errorf("lookupField(%q): expected %q, got nil", tt.key, tt.wantName)
		} else if fi.JSONName != tt.wantName {
			t.Errorf("lookupField(%q): expected %q, got %q", tt.key, tt.wantName, fi.JSONName)
		}
	}
}

func TestLookup_CaseInsensitive_Bitmap(t *testing.T) {
	names := []string{"id", "name", "email", "phone", "address", "city", "state", "zip"}
	dec := makeTestStructCodec(names)

	// Lookup with various casings
	for _, name := range names {
		// Original case
		fi := lookupField(dec, name)
		if fi == nil || fi.JSONName != name {
			t.Errorf("lookupField(%q) failed", name)
		}

		// Uppercase
		fi = lookupField(dec, toLowerASCII(name)) // already lower, but test the path
		if fi == nil || fi.JSONName != name {
			t.Errorf("lookupField(lower(%q)) failed", name)
		}
	}

	// UPPERCASE variants
	fi := lookupField(dec, "ID")
	if fi == nil || fi.JSONName != "id" {
		t.Error("expected 'id' for 'ID'")
	}
	fi = lookupField(dec, "NAME")
	if fi == nil || fi.JSONName != "name" {
		t.Error("expected 'name' for 'NAME'")
	}
}

// Edge Case Tests

func TestLookup_UnknownKeys(t *testing.T) {
	dec := makeTestStructCodec([]string{"id", "name", "email", "phone", "address"})

	unknowns := []string{"", "x", "unknown", "idd", "nam", "emaill", "PHONE2"}
	for _, key := range unknowns {
		if fi := lookupField(dec, key); fi != nil {
			t.Errorf("lookupField(%q): expected nil, got %q", key, fi.JSONName)
		}
	}
}

func TestLookup_SimilarNames(t *testing.T) {
	// Names that differ only in one character — stress-test hash quality
	dec := makeTestStructCodec([]string{
		"created_at", "created_by",
		"updated_at", "updated_by",
		"deleted_at", "deleted_by",
	})

	for _, name := range []string{"created_at", "created_by", "updated_at", "updated_by", "deleted_at", "deleted_by"} {
		fi := lookupField(dec, name)
		if fi == nil {
			t.Errorf("expected to find %q", name)
		} else if fi.JSONName != name {
			t.Errorf("expected %q, got %q", name, fi.JSONName)
		}
	}
}

func TestLookup_DuplicateLengthNames(t *testing.T) {
	// All same length — simpleMixer must differentiate by character content
	dec := makeTestStructCodec([]string{"ab", "cd", "ef", "gh", "ij"})

	for _, name := range []string{"ab", "cd", "ef", "gh", "ij"} {
		fi := lookupField(dec, name)
		if fi == nil {
			t.Errorf("expected to find %q", name)
		} else if fi.JSONName != name {
			t.Errorf("expected %q, got %q", name, fi.JSONName)
		}
	}
}

func TestLookup_SingleCharFields(t *testing.T) {
	dec := makeTestStructCodec([]string{"a", "b", "c", "d", "e", "f"})
	for _, name := range []string{"a", "b", "c", "d", "e", "f"} {
		fi := lookupField(dec, name)
		if fi == nil || fi.JSONName != name {
			t.Errorf("lookupField(%q) failed", name)
		}
	}
}

func TestLookup_UnicodeFieldNames(t *testing.T) {
	dec := makeTestStructCodec([]string{"名前", "年齢", "メール", "住所", "電話"})
	for _, name := range []string{"名前", "年齢", "メール", "住所", "電話"} {
		fi := lookupField(dec, name)
		if fi == nil || fi.JSONName != name {
			t.Errorf("lookupField(%q) failed", name)
		}
	}
}

func TestLookup_RealisticStruct(t *testing.T) {
	// Simulates a typical API response struct
	names := []string{
		"id", "type", "name", "email", "phone",
		"address", "city", "state", "zip", "country",
		"created_at", "updated_at", "is_active",
	}
	dec := makeTestStructCodec(names)

	for _, name := range names {
		fi := lookupField(dec, name)
		if fi == nil || fi.JSONName != name {
			t.Errorf("lookupField(%q) failed", name)
		}
	}

	// Case-insensitive
	if fi := lookupField(dec, "Created_At"); fi == nil || fi.JSONName != "created_at" {
		t.Error("case-insensitive lookup for Created_At failed")
	}
}

// Integration with reflect

func TestLookup_ViaReflect(t *testing.T) {
	type User struct {
		ID    int    `json:"id"`
		Name  string `json:"name"`
		Email string `json:"email"`
	}

	dec := getCodec(reflect.TypeOf(User{})).Codec.(*StructCodec)

	tests := []struct {
		key  string
		want string
	}{
		{"id", "id"},
		{"ID", "id"},
		{"name", "name"},
		{"Name", "name"},
		{"email", "email"},
		{"EMAIL", "email"},
	}

	for _, tt := range tests {
		fi := lookupField(dec, tt.key)
		if fi == nil {
			t.Errorf("lookupField(%q): expected %q, got nil", tt.key, tt.want)
		} else if fi.JSONName != tt.want {
			t.Errorf("lookupField(%q): expected %q, got %q", tt.key, tt.want, fi.JSONName)
		}
	}
}

func TestLookup_ViaReflect_LargeStruct(t *testing.T) {
	type BigStruct struct {
		F1  string `json:"f1"`
		F2  string `json:"f2"`
		F3  string `json:"f3"`
		F4  string `json:"f4"`
		F5  string `json:"f5"`
		F6  string `json:"f6"`
		F7  string `json:"f7"`
		F8  string `json:"f8"`
		F9  string `json:"f9"`
		F10 string `json:"f10"`
		F11 string `json:"f11"`
		F12 string `json:"f12"`
		F13 string `json:"f13"`
		F14 string `json:"f14"`
		F15 string `json:"f15"`
		F16 string `json:"f16"`
	}

	dec := getCodec(reflect.TypeOf(BigStruct{})).Codec.(*StructCodec)

	switch dec.Lookup.(type) {
	case *perfectSimpleLookup, *perfectFNVLookup, *perfectMulaccLookup:
		// ok
	default:
		t.Fatalf("expected perfect hash lookup for 16-field struct, got %T", dec.Lookup)
	}

	for i := 1; i <= 16; i++ {
		name := fmt.Sprintf("f%d", i)
		fi := lookupField(dec, name)
		if fi == nil {
			t.Errorf("expected to find %q", name)
		} else if fi.JSONName != name {
			t.Errorf("expected %q, got %q", name, fi.JSONName)
		}
	}
}

// lookupFieldBytes Tests

func TestLookupFieldBytes(t *testing.T) {
	dec := makeTestStructCodec([]string{"id", "name", "email", "phone", "address"})

	tests := []struct {
		key  []byte
		want string
	}{
		{[]byte("id"), "id"},
		{[]byte("ID"), "id"},
		{[]byte("Name"), "name"},
		{[]byte("EMAIL"), "email"},
		{[]byte("nonexistent"), ""},
	}

	for _, tt := range tests {
		fi := dec.LookupFieldBytes(tt.key)
		if tt.want == "" {
			if fi != nil {
				t.Errorf("LookupFieldBytes(%q): expected nil, got %q", tt.key, fi.JSONName)
			}
		} else {
			if fi == nil {
				t.Errorf("LookupFieldBytes(%q): expected %q, got nil", tt.key, tt.want)
			} else if fi.JSONName != tt.want {
				t.Errorf("LookupFieldBytes(%q): expected %q, got %q", tt.key, tt.want, fi.JSONName)
			}
		}
	}
}

// Benchmarks

// BenchmarkLookup compares linear, perfect hash, and map lookup across field counts.
func BenchmarkLookup_Linear_4fields(b *testing.B) {
	dec := makeTestStructCodec([]string{"id", "name", "email", "age"})
	key := []byte("email")
	b.ResetTimer()
	for range b.N {
		dec.LookupFieldBytes(key)
	}
}

func BenchmarkLookup_Bitmap_8fields(b *testing.B) {
	dec := makeTestStructCodec([]string{"id", "name", "email", "phone", "address", "city", "state", "zip"})
	key := []byte("address")
	b.ResetTimer()
	for range b.N {
		dec.LookupFieldBytes(key)
	}
}

func BenchmarkLookup_PerfectHash_16fields(b *testing.B) {
	names := make([]string, 16)
	for i := range 16 {
		names[i] = fmt.Sprintf("field_%d", i)
	}
	dec := makeTestStructCodec(names)
	key := []byte("field_12")
	b.ResetTimer()
	for range b.N {
		dec.LookupFieldBytes(key)
	}
}

func BenchmarkLookup_Map_40fields(b *testing.B) {
	names := make([]string, 40)
	for i := range 40 {
		names[i] = fmt.Sprintf("field_%d", i)
	}
	dec := makeTestStructCodec(names)
	key := []byte("field_30")
	b.ResetTimer()
	for range b.N {
		dec.LookupFieldBytes(key)
	}
}

func BenchmarkLookup_Miss_Bitmap(b *testing.B) {
	dec := makeTestStructCodec([]string{"id", "name", "email", "phone", "address", "city", "state", "zip"})
	key := []byte("nonexistent")
	b.ResetTimer()
	for range b.N {
		dec.LookupFieldBytes(key)
	}
}

// Baseline: raw Go map lookup for comparison
func BenchmarkLookup_RawMap_8fields(b *testing.B) {
	names := []string{"id", "name", "email", "phone", "address", "city", "state", "zip"}
	m := make(map[string]*TypeInfo, len(names))
	fields := make([]TypeInfo, len(names))
	for i, name := range names {
		fields[i] = TypeInfo{JSONName: name}
		m[name] = &fields[i]
	}
	key := "address"
	b.ResetTimer()
	for range b.N {
		_ = m[key]
	}
}

func BenchmarkLookup_RawMap_16fields(b *testing.B) {
	names := make([]string, 16)
	fields := make([]TypeInfo, 16)
	m := make(map[string]*TypeInfo, 16)
	for i := range 16 {
		names[i] = fmt.Sprintf("field_%d", i)
		fields[i] = TypeInfo{JSONName: names[i]}
		m[names[i]] = &fields[i]
	}
	key := "field_12"
	b.ResetTimer()
	for range b.N {
		_ = m[key]
	}
}
