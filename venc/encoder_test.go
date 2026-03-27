package venc

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

func TestEncoder_Basic(t *testing.T) {
	var buf bytes.Buffer
	enc := NewEncoder(&buf)

	type Msg struct {
		Name string `json:"name"`
		Age  int    `json:"age"`
	}
	v := Msg{Name: "Alice", Age: 30}
	if err := enc.Encode(&v); err != nil {
		t.Fatal(err)
	}

	got := buf.String()
	want := `{"name":"Alice","age":30}` + "\n"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestEncoder_MultipleValues(t *testing.T) {
	var buf bytes.Buffer
	enc := NewEncoder(&buf)

	for i := range 3 {
		if err := enc.Encode(&i); err != nil {
			t.Fatalf("encode %d: %v", i, err)
		}
	}

	got := buf.String()
	want := "0\n1\n2\n"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestEncoder_Indent(t *testing.T) {
	var buf bytes.Buffer
	enc := NewEncoder(&buf, EncoderSetIndent("", "  "))

	type Msg struct {
		X int `json:"x"`
	}
	v := Msg{X: 1}
	if err := enc.Encode(&v); err != nil {
		t.Fatal(err)
	}

	// Compare against stdlib
	var stdBuf bytes.Buffer
	stdEnc := json.NewEncoder(&stdBuf)
	stdEnc.SetIndent("", "  ")
	stdEnc.SetEscapeHTML(false)
	stdEnc.Encode(v)

	if buf.String() != stdBuf.String() {
		t.Errorf("vjson:\n%s\nstdlib:\n%s", buf.String(), stdBuf.String())
	}
}

func TestEncoder_SetIndent_PostCreation(t *testing.T) {
	var buf bytes.Buffer
	enc := NewEncoder(&buf)

	// First encode: compact
	v := 42
	enc.Encode(&v)

	// Switch to indented
	enc.SetIndent("", "\t")

	type Pair struct {
		A int `json:"a"`
		B int `json:"b"`
	}
	p := Pair{A: 1, B: 2}
	enc.Encode(&p)

	lines := strings.Split(buf.String(), "\n")
	// First line should be compact "42"
	if lines[0] != "42" {
		t.Errorf("first value: got %q, want %q", lines[0], "42")
	}
	// Second value should be indented (starts with '{')
	if !strings.Contains(buf.String(), "\t") {
		t.Error("indented value should contain tab characters")
	}
}

func TestEncoder_EscapeHTML(t *testing.T) {
	var buf bytes.Buffer
	enc := NewEncoder(&buf, EncoderSetEscapeHTML(true))

	s := "<script>alert('xss')</script>"
	if err := enc.Encode(&s); err != nil {
		t.Fatal(err)
	}

	got := buf.String()
	if strings.Contains(got, "<script>") {
		t.Errorf("HTML should be escaped, got: %s", got)
	}
	if !strings.Contains(got, `\u003c`) {
		t.Errorf("expected \\u003c escape, got: %s", got)
	}
}

func TestEncoder_SetEscapeHTML_PostCreation(t *testing.T) {
	var buf bytes.Buffer
	enc := NewEncoder(&buf)

	// Default: no HTML escaping
	s := "<b>"
	enc.Encode(&s)
	if strings.Contains(buf.String(), `\u003c`) {
		t.Error("default should not escape HTML")
	}

	buf.Reset()
	enc.SetEscapeHTML(true)
	enc.Encode(&s)
	if !strings.Contains(buf.String(), `\u003c`) {
		t.Error("after SetEscapeHTML(true), should escape HTML")
	}
}

func TestEncoder_NilPointer(t *testing.T) {
	var buf bytes.Buffer
	enc := NewEncoder(&buf)

	var p *int
	if err := enc.Encode(p); err != nil {
		t.Fatal(err)
	}

	got := strings.TrimSpace(buf.String())
	if got != "null" {
		t.Errorf("got %q, want %q", got, "null")
	}
}

func TestEncoder_NonPointerValue(t *testing.T) {
	var buf bytes.Buffer
	enc := NewEncoder(&buf)

	// Pass non-pointer value directly
	if err := enc.Encode(42); err != nil {
		t.Fatal(err)
	}

	got := strings.TrimSpace(buf.String())
	if got != "42" {
		t.Errorf("got %q, want %q", got, "42")
	}
}

func TestEncoder_StickyError(t *testing.T) {
	w := &failWriter{failAt: 1}
	enc := NewEncoder(w)

	v := 1
	// First write succeeds
	if err := enc.Encode(&v); err != nil {
		t.Fatalf("first encode should succeed: %v", err)
	}

	// Second write fails
	err := enc.Encode(&v)
	if err == nil {
		t.Fatal("expected error on second encode")
	}

	// Third write should return the same sticky error
	err2 := enc.Encode(&v)
	if err2 != err {
		t.Errorf("expected sticky error %v, got %v", err, err2)
	}
}

type failWriter struct {
	writes int
	failAt int
}

func (w *failWriter) Write(p []byte) (int, error) {
	w.writes++
	if w.writes > w.failAt {
		return 0, &writeErr{}
	}
	return len(p), nil
}

type writeErr struct{}

func (e *writeErr) Error() string { return "write failed" }

func TestEncoder_StdlibCompat_Struct(t *testing.T) {
	type Item struct {
		ID    int      `json:"id"`
		Name  string   `json:"name"`
		Tags  []string `json:"tags"`
		Score float64  `json:"score"`
	}

	v := Item{
		ID:    1,
		Name:  "test",
		Tags:  []string{"a", "b"},
		Score: 3.14,
	}

	var stdBuf bytes.Buffer
	stdEnc := json.NewEncoder(&stdBuf)
	stdEnc.SetEscapeHTML(false)
	stdEnc.Encode(v)

	var vjBuf bytes.Buffer
	vjEnc := NewEncoder(&vjBuf)
	vjEnc.Encode(&v)

	if vjBuf.String() != stdBuf.String() {
		t.Errorf("vjson: %s\nstdlib: %s", vjBuf.String(), stdBuf.String())
	}
}

func TestEncoder_StdlibCompat_Map(t *testing.T) {
	v := map[string]any{
		"key": "value",
		"num": 42.0,
	}

	var stdBuf bytes.Buffer
	stdEnc := json.NewEncoder(&stdBuf)
	stdEnc.SetEscapeHTML(false)
	stdEnc.Encode(v)

	var vjBuf bytes.Buffer
	vjEnc := NewEncoder(&vjBuf)
	vjEnc.Encode(&v)

	// Parse both to compare (map order may differ)
	var stdVal, vjVal map[string]any
	json.Unmarshal(stdBuf.Bytes(), &stdVal)
	json.Unmarshal(vjBuf.Bytes(), &vjVal)

	if stdVal["key"] != vjVal["key"] || stdVal["num"] != vjVal["num"] {
		t.Errorf("vjson: %s\nstdlib: %s", vjBuf.String(), stdBuf.String())
	}
}

// TestEncoder_MapNonPointer exercises the Encode path for map values passed
// directly (not as *map). Maps are "direct interface" types in Go — the eface
// data word IS the *hmap pointer, not a pointer to it. This previously caused
// a "traceback did not unwind completely" crash because the encoding path
// dereferenced one level too deep.
func TestEncoder_MapNonPointer(t *testing.T) {
	t.Run("map[string]string", func(t *testing.T) {
		var buf bytes.Buffer
		enc := NewEncoder(&buf)
		m := map[string]string{"code": "INVALID_LIMIT", "message": "limit must be >= 1"}
		if err := enc.Encode(m); err != nil {
			t.Fatal(err)
		}
		// Verify the output is valid JSON by round-tripping.
		var got map[string]string
		if err := json.Unmarshal(buf.Bytes(), &got); err != nil {
			t.Fatalf("output is not valid JSON: %v\nraw: %s", err, buf.String())
		}
		if got["code"] != "INVALID_LIMIT" || got["message"] != "limit must be >= 1" {
			t.Errorf("unexpected result: %v", got)
		}
	})

	t.Run("map[string]any", func(t *testing.T) {
		var buf bytes.Buffer
		enc := NewEncoder(&buf)
		m := map[string]any{"key": "value", "num": 42.0}
		if err := enc.Encode(m); err != nil {
			t.Fatal(err)
		}
		var got map[string]any
		if err := json.Unmarshal(buf.Bytes(), &got); err != nil {
			t.Fatalf("output is not valid JSON: %v\nraw: %s", err, buf.String())
		}
		if got["key"] != "value" || got["num"] != 42.0 {
			t.Errorf("unexpected result: %v", got)
		}
	})

	t.Run("map[string]int", func(t *testing.T) {
		var buf bytes.Buffer
		enc := NewEncoder(&buf)
		m := map[string]int{"a": 1, "b": 2}
		if err := enc.Encode(m); err != nil {
			t.Fatal(err)
		}
		var got map[string]int
		if err := json.Unmarshal(buf.Bytes(), &got); err != nil {
			t.Fatalf("output is not valid JSON: %v\nraw: %s", err, buf.String())
		}
		if got["a"] != 1 || got["b"] != 2 {
			t.Errorf("unexpected result: %v", got)
		}
	})

	t.Run("nil map", func(t *testing.T) {
		var buf bytes.Buffer
		enc := NewEncoder(&buf)
		var m map[string]string
		if err := enc.Encode(m); err != nil {
			t.Fatal(err)
		}
		got := strings.TrimSpace(buf.String())
		if got != "null" {
			t.Errorf("got %q, want %q", got, "null")
		}
	})

	t.Run("empty map", func(t *testing.T) {
		var buf bytes.Buffer
		enc := NewEncoder(&buf)
		m := map[string]string{}
		if err := enc.Encode(m); err != nil {
			t.Fatal(err)
		}
		got := strings.TrimSpace(buf.String())
		if got != "{}" {
			t.Errorf("got %q, want %q", got, "{}")
		}
	})
}

// TestEncoder_SliceNonPointer ensures slices passed by value to Encode work.
func TestEncoder_SliceNonPointer(t *testing.T) {
	var buf bytes.Buffer
	enc := NewEncoder(&buf)
	s := []int{1, 2, 3}
	if err := enc.Encode(s); err != nil {
		t.Fatal(err)
	}
	got := strings.TrimSpace(buf.String())
	if got != "[1,2,3]" {
		t.Errorf("got %q, want %q", got, "[1,2,3]")
	}
}

// TestEncoder_StructNonPointer ensures structs passed by value to Encode work.
func TestEncoder_StructNonPointer(t *testing.T) {
	var buf bytes.Buffer
	enc := NewEncoder(&buf)
	type Msg struct {
		Name string `json:"name"`
	}
	if err := enc.Encode(Msg{Name: "test"}); err != nil {
		t.Fatal(err)
	}
	got := strings.TrimSpace(buf.String())
	if got != `{"name":"test"}` {
		t.Errorf("got %q, want %q", got, `{"name":"test"}`)
	}
}

func TestEncodeValue_Generic(t *testing.T) {
	var buf bytes.Buffer
	enc := NewEncoder(&buf)

	s := "hello"
	if err := EncodeValue(enc, &s); err != nil {
		t.Fatal(err)
	}

	got := strings.TrimSpace(buf.String())
	if got != `"hello"` {
		t.Errorf("got %q, want %q", got, `"hello"`)
	}
}
