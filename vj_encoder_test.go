package vjson

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

func TestEncoder_NDJSON_Stream(t *testing.T) {
	var buf bytes.Buffer
	enc := NewEncoder(&buf)

	type Record struct {
		V int `json:"v"`
	}

	for i := range 5 {
		r := Record{V: i}
		if err := enc.Encode(&r); err != nil {
			t.Fatalf("encode %d: %v", i, err)
		}
	}

	// Verify: decode with Decoder
	dec := NewDecoder(strings.NewReader(buf.String()))
	for i := range 5 {
		var r Record
		if err := dec.Decode(&r); err != nil {
			t.Fatalf("decode %d: %v", i, err)
		}
		if r.V != i {
			t.Errorf("decode %d: got V=%d, want %d", i, r.V, i)
		}
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
