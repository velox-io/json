package vjson

import (
	"fmt"
	"reflect"
	"testing"
)

// =============================================================================
// Test Helpers
// =============================================================================

// makeTestStructDecoder builds a ReflectStructDecoder from a list of field names.
// This bypasses reflect to create controlled test scenarios.
func makeTestStructDecoder(names []string) *ReflectStructDecoder {
	dec := &ReflectStructDecoder{
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

// =============================================================================
// toLowerASCII Tests
// =============================================================================

func TestToLowerASCII_AllLower(t *testing.T) {
	s := "hello_world"
	result := toLowerASCII(s)
	if result != "hello_world" {
		t.Errorf("expected hello_world, got %s", result)
	}
	// Should return the same string pointer (zero alloc)
	if &([]byte(s))[0] != &([]byte(result))[0] {
		// Note: Go string comparison — if no mutation, same string should be returned
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

// =============================================================================
// Build Correctness Tests
// =============================================================================

func TestBuildLookup_Empty(t *testing.T) {
	dec := makeTestStructDecoder(nil)
	if dec.LookupFn == nil {
		t.Fatal("LookupFn should not be nil")
	}
	if fi := dec.LookupField("anything"); fi != nil {
		t.Error("expected nil for empty struct")
	}
}

func TestBuildLookup_SingleField(t *testing.T) {
	dec := makeTestStructDecoder([]string{"id"})
	fi := dec.LookupField("id")
	if fi == nil {
		t.Fatal("expected to find 'id'")
	}
	if fi.JSONName != "id" {
		t.Errorf("expected JSONName='id', got %q", fi.JSONName)
	}
}

func TestBuildLookup_LinearRange(t *testing.T) {
	// 1-4 fields should use linear scan
	for n := 1; n <= 4; n++ {
		names := make([]string, n)
		for i := range n {
			names[i] = fmt.Sprintf("field%d", i)
		}
		dec := makeTestStructDecoder(names)

		for _, name := range names {
			fi := dec.LookupField(name)
			if fi == nil {
				t.Errorf("n=%d: expected to find %q", n, name)
			} else if fi.JSONName != name {
				t.Errorf("n=%d: expected %q, got %q", n, name, fi.JSONName)
			}
		}

		// Unknown key
		if fi := dec.LookupField("nonexistent"); fi != nil {
			t.Errorf("n=%d: expected nil for unknown key", n)
		}
	}
}

func TestBuildLookup_PerfectHashRange(t *testing.T) {
	// 5-32 fields should use perfect hash
	for _, n := range []int{5, 8, 12, 16, 20, 24, 28, 32} {
		names := make([]string, n)
		for i := range n {
			names[i] = fmt.Sprintf("field_%d", i)
		}
		dec := makeTestStructDecoder(names)

		if dec.HashTable == nil {
			t.Errorf("n=%d: expected perfect hash table", n)
			continue
		}

		for _, name := range names {
			fi := dec.LookupField(name)
			if fi == nil {
				t.Errorf("n=%d: expected to find %q", n, name)
			} else if fi.JSONName != name {
				t.Errorf("n=%d: expected %q, got %q", n, name, fi.JSONName)
			}
		}

		if fi := dec.LookupField("nonexistent"); fi != nil {
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
	dec := makeTestStructDecoder(names)

	if dec.FieldMap == nil {
		t.Fatal("expected FieldMap for 40 fields")
	}

	for _, name := range names {
		fi := dec.LookupField(name)
		if fi == nil {
			t.Errorf("expected to find %q", name)
		}
	}
}

// =============================================================================
// Case-Insensitive Lookup Tests
// =============================================================================

func TestLookup_CaseInsensitive(t *testing.T) {
	dec := makeTestStructDecoder([]string{"Name", "Age", "Email"})

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
		fi := dec.LookupField(tt.key)
		if fi == nil {
			t.Errorf("LookupField(%q): expected %q, got nil", tt.key, tt.wantName)
		} else if fi.JSONName != tt.wantName {
			t.Errorf("LookupField(%q): expected %q, got %q", tt.key, tt.wantName, fi.JSONName)
		}
	}
}

func TestLookup_CaseInsensitive_PerfectHash(t *testing.T) {
	names := []string{"id", "name", "email", "phone", "address", "city", "state", "zip"}
	dec := makeTestStructDecoder(names)

	// Lookup with various casings
	for _, name := range names {
		// Original case
		fi := dec.LookupField(name)
		if fi == nil || fi.JSONName != name {
			t.Errorf("LookupField(%q) failed", name)
		}

		// Uppercase
		fi = dec.LookupField(toLowerASCII(name)) // already lower, but test the path
		if fi == nil || fi.JSONName != name {
			t.Errorf("LookupField(lower(%q)) failed", name)
		}
	}

	// UPPERCASE variants
	fi := dec.LookupField("ID")
	if fi == nil || fi.JSONName != "id" {
		t.Error("expected 'id' for 'ID'")
	}
	fi = dec.LookupField("NAME")
	if fi == nil || fi.JSONName != "name" {
		t.Error("expected 'name' for 'NAME'")
	}
}

// =============================================================================
// Edge Case Tests
// =============================================================================

func TestLookup_UnknownKeys(t *testing.T) {
	dec := makeTestStructDecoder([]string{"id", "name", "email", "phone", "address"})

	unknowns := []string{"", "x", "unknown", "idd", "nam", "emaill", "PHONE2"}
	for _, key := range unknowns {
		if fi := dec.LookupField(key); fi != nil {
			t.Errorf("LookupField(%q): expected nil, got %q", key, fi.JSONName)
		}
	}
}

func TestLookup_SimilarNames(t *testing.T) {
	// Names that differ only in one character — stress-test hash quality
	dec := makeTestStructDecoder([]string{
		"created_at", "created_by",
		"updated_at", "updated_by",
		"deleted_at", "deleted_by",
	})

	for _, name := range []string{"created_at", "created_by", "updated_at", "updated_by", "deleted_at", "deleted_by"} {
		fi := dec.LookupField(name)
		if fi == nil {
			t.Errorf("expected to find %q", name)
		} else if fi.JSONName != name {
			t.Errorf("expected %q, got %q", name, fi.JSONName)
		}
	}
}

func TestLookup_DuplicateLengthNames(t *testing.T) {
	// All same length — simpleMixer must differentiate by character content
	dec := makeTestStructDecoder([]string{"ab", "cd", "ef", "gh", "ij"})

	for _, name := range []string{"ab", "cd", "ef", "gh", "ij"} {
		fi := dec.LookupField(name)
		if fi == nil {
			t.Errorf("expected to find %q", name)
		} else if fi.JSONName != name {
			t.Errorf("expected %q, got %q", name, fi.JSONName)
		}
	}
}

func TestLookup_SingleCharFields(t *testing.T) {
	dec := makeTestStructDecoder([]string{"a", "b", "c", "d", "e", "f"})
	for _, name := range []string{"a", "b", "c", "d", "e", "f"} {
		fi := dec.LookupField(name)
		if fi == nil || fi.JSONName != name {
			t.Errorf("LookupField(%q) failed", name)
		}
	}
}

func TestLookup_UnicodeFieldNames(t *testing.T) {
	dec := makeTestStructDecoder([]string{"名前", "年齢", "メール", "住所", "電話"})
	for _, name := range []string{"名前", "年齢", "メール", "住所", "電話"} {
		fi := dec.LookupField(name)
		if fi == nil || fi.JSONName != name {
			t.Errorf("LookupField(%q) failed", name)
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
	dec := makeTestStructDecoder(names)

	for _, name := range names {
		fi := dec.LookupField(name)
		if fi == nil || fi.JSONName != name {
			t.Errorf("LookupField(%q) failed", name)
		}
	}

	// Case-insensitive
	if fi := dec.LookupField("Created_At"); fi == nil || fi.JSONName != "created_at" {
		t.Error("case-insensitive lookup for Created_At failed")
	}
}

// =============================================================================
// Integration with reflect
// =============================================================================

func TestLookup_ViaReflect(t *testing.T) {
	type User struct {
		ID    int    `json:"id"`
		Name  string `json:"name"`
		Email string `json:"email"`
	}

	dec := GetDecoder(reflect.TypeOf(User{})).Decoder.(*ReflectStructDecoder)

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
		fi := dec.LookupField(tt.key)
		if fi == nil {
			t.Errorf("LookupField(%q): expected %q, got nil", tt.key, tt.want)
		} else if fi.JSONName != tt.want {
			t.Errorf("LookupField(%q): expected %q, got %q", tt.key, tt.want, fi.JSONName)
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

	dec := GetDecoder(reflect.TypeOf(BigStruct{})).Decoder.(*ReflectStructDecoder)

	if dec.HashTable == nil {
		t.Fatal("expected perfect hash for 16-field struct")
	}

	for i := 1; i <= 16; i++ {
		name := fmt.Sprintf("f%d", i)
		fi := dec.LookupField(name)
		if fi == nil {
			t.Errorf("expected to find %q", name)
		} else if fi.JSONName != name {
			t.Errorf("expected %q, got %q", name, fi.JSONName)
		}
	}
}

// =============================================================================
// lookupFieldBytes Tests
// =============================================================================

func TestLookupFieldBytes(t *testing.T) {
	dec := makeTestStructDecoder([]string{"id", "name", "email", "phone", "address"})
	scratch := make([]byte, 64)

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
		fi := dec.LookupFieldBytes(tt.key, scratch)
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

// =============================================================================
// Benchmarks
// =============================================================================

// BenchmarkLookup compares linear, perfect hash, and map lookup across field counts.
func BenchmarkLookup_Linear_4fields(b *testing.B) {
	dec := makeTestStructDecoder([]string{"id", "name", "email", "age"})
	key := "email"
	b.ResetTimer()
	for range b.N {
		dec.LookupField(key)
	}
}

func BenchmarkLookup_PerfectHash_8fields(b *testing.B) {
	dec := makeTestStructDecoder([]string{"id", "name", "email", "phone", "address", "city", "state", "zip"})
	key := "address"
	b.ResetTimer()
	for range b.N {
		dec.LookupField(key)
	}
}

func BenchmarkLookup_PerfectHash_16fields(b *testing.B) {
	names := make([]string, 16)
	for i := range 16 {
		names[i] = fmt.Sprintf("field_%d", i)
	}
	dec := makeTestStructDecoder(names)
	key := "field_12"
	b.ResetTimer()
	for range b.N {
		dec.LookupField(key)
	}
}

func BenchmarkLookup_Map_40fields(b *testing.B) {
	names := make([]string, 40)
	for i := range 40 {
		names[i] = fmt.Sprintf("field_%d", i)
	}
	dec := makeTestStructDecoder(names)
	key := "field_30"
	b.ResetTimer()
	for range b.N {
		dec.LookupField(key)
	}
}

func BenchmarkLookup_Miss_PerfectHash(b *testing.B) {
	dec := makeTestStructDecoder([]string{"id", "name", "email", "phone", "address", "city", "state", "zip"})
	key := "nonexistent"
	b.ResetTimer()
	for range b.N {
		dec.LookupField(key)
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

func BenchmarkLookupFieldBytes_8fields(b *testing.B) {
	dec := makeTestStructDecoder([]string{"id", "name", "email", "phone", "address", "city", "state", "zip"})
	key := []byte("address")
	scratch := make([]byte, 64)
	b.ResetTimer()
	for range b.N {
		dec.LookupFieldBytes(key, scratch)
	}
}

func BenchmarkToLowerASCII_NoUpper(b *testing.B) {
	s := "created_at"
	b.ResetTimer()
	for range b.N {
		toLowerASCII(s)
	}
}

func BenchmarkToLowerASCII_WithUpper(b *testing.B) {
	s := "CreatedAt"
	b.ResetTimer()
	for range b.N {
		toLowerASCII(s)
	}
}
