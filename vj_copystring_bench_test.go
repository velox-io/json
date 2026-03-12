package vjson

import "testing"

// Benchmark payloads of varying sizes and string densities.
var (
	// Tiny: few short strings
	benchTinyJSON = []byte(`{"id":1,"name":"alice","active":true}`)

	// Medium: many string fields (string-heavy struct)
	benchMediumJSON = []byte(`{
		"firstName":"John","lastName":"Doe","email":"john.doe@example.com",
		"phone":"+1-555-0123","address":"123 Main St, Springfield, IL 62701",
		"company":"Acme Corp","title":"Senior Engineer","department":"Platform",
		"bio":"A passionate developer with 10 years of experience in distributed systems",
		"website":"https://johndoe.dev"
	}`)

	// Large map: map[string]string with many entries
	benchMapJSON = []byte(`{
		"key1":"value1","key2":"value2","key3":"value3","key4":"value4",
		"key5":"value5","key6":"value6","key7":"value7","key8":"value8",
		"key9":"value9","key10":"value10","key11":"value11","key12":"value12",
		"key13":"value13","key14":"value14","key15":"value15","key16":"value16"
	}`)

	// Escaped strings: force unescape path
	benchEscapedJSON = []byte(`{
		"msg":"line1\nline2\nline3\ttab\there",
		"path":"C:\\Users\\john\\Documents\\file.txt",
		"html":"<div class=\"test\">&amp; foo</div>"
	}`)

	// Any/interface: strings in interface{} fields
	benchAnyJSON = []byte(`{"name":"test","tags":["alpha","beta","gamma"],"meta":{"env":"prod","region":"us-east-1"}}`)
)

type benchTiny struct {
	ID     int    `json:"id"`
	Name   string `json:"name"`
	Active bool   `json:"active"`
}

type benchMedium struct {
	FirstName  string `json:"firstName"`
	LastName   string `json:"lastName"`
	Email      string `json:"email"`
	Phone      string `json:"phone"`
	Address    string `json:"address"`
	Company    string `json:"company"`
	Title      string `json:"title"`
	Department string `json:"department"`
	Bio        string `json:"bio"`
	Website    string `json:"website"`
}

type benchEscaped struct {
	Msg  string `json:"msg"`
	Path string `json:"path"`
	HTML string `json:"html"`
}

type benchAny struct {
	Name string `json:"name"`
	Tags []any  `json:"tags"`
	Meta any    `json:"meta"`
}

// --- Tiny ---

func BenchmarkCopyString_Tiny_ZeroCopy(b *testing.B) {
	b.ReportAllocs()
	for b.Loop() {
		var v benchTiny
		if err := Unmarshal(benchTinyJSON, &v); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkCopyString_Tiny_CopyAll(b *testing.B) {
	b.ReportAllocs()
	for b.Loop() {
		var v benchTiny
		if err := Unmarshal(benchTinyJSON, &v, WithCopyString()); err != nil {
			b.Fatal(err)
		}
	}
}

// --- Medium (string-heavy) ---

func BenchmarkCopyString_Medium_ZeroCopy(b *testing.B) {
	b.ReportAllocs()
	for b.Loop() {
		var v benchMedium
		if err := Unmarshal(benchMediumJSON, &v); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkCopyString_Medium_CopyAll(b *testing.B) {
	b.ReportAllocs()
	for b.Loop() {
		var v benchMedium
		if err := Unmarshal(benchMediumJSON, &v, WithCopyString()); err != nil {
			b.Fatal(err)
		}
	}
}

// --- Map[string]string ---

func BenchmarkCopyString_Map_ZeroCopy(b *testing.B) {
	b.ReportAllocs()
	for b.Loop() {
		var m map[string]string
		if err := Unmarshal(benchMapJSON, &m); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkCopyString_Map_CopyAll(b *testing.B) {
	b.ReportAllocs()
	for b.Loop() {
		var m map[string]string
		if err := Unmarshal(benchMapJSON, &m, WithCopyString()); err != nil {
			b.Fatal(err)
		}
	}
}

// --- Escaped strings ---

func BenchmarkCopyString_Escaped_ZeroCopy(b *testing.B) {
	b.ReportAllocs()
	for b.Loop() {
		var v benchEscaped
		if err := Unmarshal(benchEscapedJSON, &v); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkCopyString_Escaped_CopyAll(b *testing.B) {
	b.ReportAllocs()
	for b.Loop() {
		var v benchEscaped
		if err := Unmarshal(benchEscapedJSON, &v, WithCopyString()); err != nil {
			b.Fatal(err)
		}
	}
}

// --- Interface{}/any ---

func BenchmarkCopyString_Any_ZeroCopy(b *testing.B) {
	b.ReportAllocs()
	for b.Loop() {
		var v benchAny
		if err := Unmarshal(benchAnyJSON, &v); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkCopyString_Any_CopyAll(b *testing.B) {
	b.ReportAllocs()
	for b.Loop() {
		var v benchAny
		if err := Unmarshal(benchAnyJSON, &v, WithCopyString()); err != nil {
			b.Fatal(err)
		}
	}
}

// --- Field-level tag (only one field copied) ---

type benchTagCopy struct {
	FirstName  string `json:"firstName,copy"`
	LastName   string `json:"lastName"`
	Email      string `json:"email"`
	Phone      string `json:"phone"`
	Address    string `json:"address"`
	Company    string `json:"company"`
	Title      string `json:"title"`
	Department string `json:"department"`
	Bio        string `json:"bio"`
	Website    string `json:"website"`
}

func BenchmarkCopyString_Medium_TagCopy1(b *testing.B) {
	b.ReportAllocs()
	for b.Loop() {
		var v benchTagCopy
		if err := Unmarshal(benchMediumJSON, &v); err != nil {
			b.Fatal(err)
		}
	}
}
