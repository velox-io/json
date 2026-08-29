package bind

import (
	"errors"
	"reflect"
	"strings"
	"testing"
	"unsafe"

	"github.com/velox-io/json/decode/dom"
	"github.com/velox-io/json/native/ndec"
	"github.com/velox-io/json/vbind"
)

// kindof test types. Each envelope has a single Data field whose concrete type
// is selected by the JSON value's kind (bool/number/string/array/object).

type kindofUser struct {
	Name string `json:"name"`
	Role string `json:"role"`
}

// kindofEnvelopeScalar has only scalar cases (bool/number/string). These all
// dispatch inline (fast path) since the case types are concrete scalars.
type kindofEnvelopeScalar struct {
	Data any `json:"data" vjson:"kindof"`
}

// kindofEnvelopeMixed has object + array + scalar cases. Object/array cases
// dispatch inline (struct/slice are concrete, not COLD). Tests struct/slice
// case dispatch via kindof_dispatch.
type kindofEnvelopeMixed struct {
	Data any `json:"data" vjson:"kindof"`
}

// kindofEnvelopePointer has a pointer case type (cold-kind: falls back to
// tape + C-side tape-bind sub-routine rebind).
type kindofEnvelopePointer struct {
	Data any `json:"data" vjson:"kindof"`
}

// kindofEnvelopeIface uses a user-defined interface as the kindof field type.
type kindofEnvelopeIface struct {
	Data kindofEventData `json:"data" vjson:"kindof"`
}

type kindofNestedErrorHost struct {
	Data  any                  `json:"data" vjson:"kindof"`
	Inner kindofEnvelopeScalar `json:"inner"`
}

// kindofEventData is a user-defined interface for testing iface kindof fields.
type kindofEventData interface {
	KindName() string
}

func (kindofUser) KindName() string { return "user" }

func init() {
	vbind.DefineKindofCases[kindofEnvelopeScalar, struct {
		bool   bool
		number float64
		string string
	}]()
	vbind.DefineKindofCases[kindofEnvelopeMixed, struct {
		bool   bool
		number float64
		string string
		array  []kindofUser
		object kindofUser
	}]()
	vbind.DefineKindofCases[kindofEnvelopePointer, struct {
		object *kindofUser
	}]()
	vbind.DefineKindofCases[kindofEnvelopeIface, struct {
		object kindofUser
	}]()
	vbind.DefineKindofCases[kindofNestedErrorHost, struct {
		bool   bool
		number float64
		string string
	}]()
}

// --- Fast path: scalar cases (inline dispatch) ---

func TestKindof_BoolCase(t *testing.T) {
	src := `{"data":true}`
	var env kindofEnvelopeScalar
	if err := Unmarshal([]byte(src), &env); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	b, ok := env.Data.(bool)
	if !ok {
		t.Fatalf("Data = %T, want bool", env.Data)
	}
	if !b {
		t.Errorf("Data = %v, want true", b)
	}
}

func TestKindof_BoolFalseCase(t *testing.T) {
	src := `{"data":false}`
	var env kindofEnvelopeScalar
	if err := Unmarshal([]byte(src), &env); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	b, ok := env.Data.(bool)
	if !ok {
		t.Fatalf("Data = %T, want bool", env.Data)
	}
	if b {
		t.Errorf("Data = %v, want false", b)
	}
}

func TestKindof_NumberCase(t *testing.T) {
	src := `{"data":42.5}`
	var env kindofEnvelopeScalar
	if err := Unmarshal([]byte(src), &env); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	n, ok := env.Data.(float64)
	if !ok {
		t.Fatalf("Data = %T, want float64", env.Data)
	}
	if n != 42.5 {
		t.Errorf("Data = %v, want 42.5", n)
	}
}

func TestKindof_StringCase(t *testing.T) {
	src := `{"data":"hello"}`
	var env kindofEnvelopeScalar
	if err := Unmarshal([]byte(src), &env); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	s, ok := env.Data.(string)
	if !ok {
		t.Fatalf("Data = %T, want string", env.Data)
	}
	if s != "hello" {
		t.Errorf("Data = %q, want %q", s, "hello")
	}
}

// --- Fast path: struct/array cases (inline dispatch via kindof_dispatch) ---

func TestKindof_ObjectCase(t *testing.T) {
	src := `{"data":{"name":"Alice","role":"admin"}}`
	var env kindofEnvelopeMixed
	if err := Unmarshal([]byte(src), &env); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	u, ok := env.Data.(kindofUser)
	if !ok {
		t.Fatalf("Data = %T, want kindofUser", env.Data)
	}
	if u.Name != "Alice" || u.Role != "admin" {
		t.Errorf("Data = %+v, want {Alice admin}", u)
	}
}

func TestKindof_ArrayCase(t *testing.T) {
	src := `{"data":[{"name":"Alice","role":"admin"},{"name":"Bob","role":"user"}]}`
	var env kindofEnvelopeMixed
	if err := Unmarshal([]byte(src), &env); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	users, ok := env.Data.([]kindofUser)
	if !ok {
		t.Fatalf("Data = %T, want []kindofUser", env.Data)
	}
	if len(users) != 2 {
		t.Fatalf("len = %d, want 2", len(users))
	}
	if users[0].Name != "Alice" || users[1].Name != "Bob" {
		t.Errorf("Data = %+v", users)
	}
}

func TestKindof_EmptyArrayCase(t *testing.T) {
	src := `{"data":[]}`
	var env kindofEnvelopeMixed
	if err := Unmarshal([]byte(src), &env); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	users, ok := env.Data.([]kindofUser)
	if !ok {
		t.Fatalf("Data = %T, want []kindofUser", env.Data)
	}
	if len(users) != 0 {
		t.Errorf("len = %d, want 0", len(users))
	}
}

// --- Null handling: null → nil eface ---

func TestKindof_NullValue(t *testing.T) {
	src := `{"data":null}`
	var env kindofEnvelopeMixed
	if err := Unmarshal([]byte(src), &env); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if env.Data != nil {
		t.Errorf("Data = %v, want nil", env.Data)
	}
}

// --- Unregistered kind error ---

func requireKindofError(t *testing.T, err error) *KindofError {
	t.Helper()
	var kindErr *KindofError
	if !errors.As(err, &kindErr) {
		t.Fatalf("error = %v, want *KindofError", err)
	}
	return kindErr
}

func TestKindof_UnregisteredKindErrorCoordinates(t *testing.T) {
	cases := []struct {
		name string
		src  string
		kind string
		pos  uint32
	}{
		{"object", `{"data":{"name":"Alice"}}`, "object", 8},
		{"array", `{"pad":0,"data":[1,2,3]}`, "array", 16},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			var jsonDst kindofEnvelopeScalar
			jsonErr := requireKindofError(t, Unmarshal([]byte(c.src), &jsonDst))
			if jsonErr.Pos != c.pos {
				t.Errorf("Unmarshal Pos = %d, want %d", jsonErr.Pos, c.pos)
			}
			if !strings.Contains(jsonErr.Message, c.kind) {
				t.Errorf("Unmarshal Message = %q, want kind %q", jsonErr.Message, c.kind)
			}

			val, err := dom.Parse([]byte(c.src))
			if err != nil {
				t.Fatalf("dom.Parse: %v", err)
			}
			var valueDst kindofEnvelopeScalar
			valueErr := requireKindofError(t, UnmarshalValue(val, &valueDst))
			if valueErr.Pos != 0 {
				t.Errorf("UnmarshalValue Pos = %d, want 0", valueErr.Pos)
			}
			if !strings.Contains(valueErr.Message, c.kind) {
				t.Errorf("UnmarshalValue Message = %q, want kind %q", valueErr.Message, c.kind)
			}
		})
	}
}

func TestKindof_NestedErrorHost(t *testing.T) {
	const src = `{"inner":{"data":{"x":1}}}`
	val, err := dom.Parse([]byte(src))
	if err != nil {
		t.Fatalf("dom.Parse: %v", err)
	}
	paths := []struct {
		name string
		run  func(*kindofNestedErrorHost) error
	}{
		{"Unmarshal", func(dst *kindofNestedErrorHost) error { return Unmarshal([]byte(src), dst) }},
		{"UnmarshalValue", func(dst *kindofNestedErrorHost) error { return UnmarshalValue(val, dst) }},
	}
	for _, path := range paths {
		t.Run(path.name, func(t *testing.T) {
			var dst kindofNestedErrorHost
			kindErr := requireKindofError(t, path.run(&dst))
			if kindErr.Host != reflect.TypeFor[kindofEnvelopeScalar]().String() {
				t.Errorf("Host = %q, want %q", kindErr.Host, reflect.TypeFor[kindofEnvelopeScalar]().String())
			}
		})
	}
}

func TestKindof_ErrorStateDoesNotLeakAcrossPaths(t *testing.T) {
	p, err := NewParser[kindofNestedErrorHost]()
	if err != nil {
		t.Fatalf("NewParser: %v", err)
	}

	var jsonDst kindofNestedErrorHost
	jsonErr := requireKindofError(t, p.Unmarshal([]byte(`{"pad":0,"data":[1]}`), &jsonDst))
	if jsonErr.Pos != 16 {
		t.Errorf("Unmarshal Pos = %d, want 16", jsonErr.Pos)
	}
	if jsonErr.Host != reflect.TypeFor[kindofNestedErrorHost]().String() {
		t.Errorf("Unmarshal Host = %q, want %q", jsonErr.Host, reflect.TypeFor[kindofNestedErrorHost]().String())
	}

	val, err := dom.Parse([]byte(`{"inner":{"data":{"x":1}}}`))
	if err != nil {
		t.Fatalf("dom.Parse: %v", err)
	}
	var valueDst kindofNestedErrorHost
	valueErr := requireKindofError(t, p.UnmarshalValue(val, &valueDst))
	if valueErr.Pos != 0 {
		t.Errorf("UnmarshalValue Pos = %d, want 0", valueErr.Pos)
	}
	if valueErr.Host != reflect.TypeFor[kindofEnvelopeScalar]().String() {
		t.Errorf("UnmarshalValue Host = %q, want %q", valueErr.Host, reflect.TypeFor[kindofEnvelopeScalar]().String())
	}
	if !strings.Contains(valueErr.Message, "object") {
		t.Errorf("UnmarshalValue Message = %q, want object kind", valueErr.Message)
	}
}

// An unregistered kind is the final answer, not a deferral: the value's own kind
// selects the case, so struct close has nothing more to learn. So the field site
// must reject it outright rather than route it to the merged tape and let phase2
// discover the same thing. That would build a tape for a doomed parse and lose
// the source offset, phase2 working in tape positions.
//
// Both assertions are tight. TapeUsed counts words this parse consumed, so any
// routing to the tape shows up as nonzero. Pos must land on the value's first
// byte, which pins the report to the field site.
func TestKindof_UnregisteredKindBuildsNoTape(t *testing.T) {
	p, err := NewParser[kindofEnvelopeScalar]()
	if err != nil {
		t.Fatalf("NewParser: %v", err)
	}
	// The arena cursor resets per parse, so this reads this parse's consumption.
	tapeUsed := func() int {
		m := (*ndec.BindMachine)(unsafe.Pointer(unsafe.SliceData(p.machine)))
		return int(m.Alloc.TapeUsed)
	}

	// kindofEnvelopeScalar registers bool/number/string only. Each unregistered
	// case pairs with a registered one to show the rejection is kind-specific
	// rather than the parser giving up on the field.
	cases := []struct {
		src     string
		wantPos uint32 // offset of the value's first byte
	}{
		{`{"data":{"name":"Alice"}}`, 8},
		{`{"data":[1,2,3]}`, 8},
		{`{"pad":0,"data":[1]}`, 16},
	}
	for _, c := range cases {
		var env kindofEnvelopeScalar
		err := p.Unmarshal([]byte(c.src), &env)
		var kerr *KindofError
		if !errors.As(err, &kerr) {
			t.Fatalf("%s: err = %v (%T), want *KindofError", c.src, err, err)
		}
		if used := tapeUsed(); used != 0 {
			t.Errorf("%s: tape used %d words; an unregistered kind must not reach the merged tape",
				c.src, used)
		}
		if kerr.Pos != c.wantPos {
			t.Errorf("%s: Pos = %d, want %d (offset of the value)", c.src, kerr.Pos, c.wantPos)
		}
	}

	// A registered kind on the same parser still binds, so the rejection above is
	// not the parser refusing the field outright.
	var env kindofEnvelopeScalar
	if err := p.Unmarshal([]byte(`{"data":true}`), &env); err != nil {
		t.Fatalf("registered kind: %v", err)
	}
	if env.Data != true {
		t.Errorf("Data = %#v, want true", env.Data)
	}
}

// A byte that starts no JSON value is a syntax error, not an unregistered kind.
// The distinction matters because the field site decides before the value is
// parsed: the case lookup sees "none of the five kinds" for garbage and for a
// genuinely unregistered kind alike, and reporting the former as a kindof
// failure would blame the schema for what is malformed input.
func TestKindof_MalformedValueIsSyntaxError(t *testing.T) {
	for _, src := range []string{
		`{"data":zzz}`,
		`{"data":}`,
		`{"data":+1}`,
		`{"data":'x'}`,
	} {
		var env kindofEnvelopeScalar
		err := Unmarshal([]byte(src), &env)
		if err == nil {
			t.Errorf("%s: expected an error, got nil", src)
			continue
		}
		var kerr *KindofError
		if errors.As(err, &kerr) {
			t.Errorf("%s: reported as kindof failure (%v); malformed input is a syntax error", src, err)
		}
	}
}

// --- Cold path: pointer case (tape + Go walker rebind) ---

func TestKindof_PointerCaseObject(t *testing.T) {
	src := `{"data":{"name":"Alice","role":"admin"}}`
	var env kindofEnvelopePointer
	if err := Unmarshal([]byte(src), &env); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	u, ok := env.Data.(*kindofUser)
	if !ok {
		t.Fatalf("Data = %T, want *kindofUser", env.Data)
	}
	if u == nil {
		t.Fatal("Data is nil *kindofUser")
	}
	if u.Name != "Alice" || u.Role != "admin" {
		t.Errorf("Data = %+v, want {Alice admin}", *u)
	}
}

func TestKindof_PointerCaseNull(t *testing.T) {
	src := `{"data":null}`
	var env kindofEnvelopePointer
	if err := Unmarshal([]byte(src), &env); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if env.Data != nil {
		t.Errorf("Data = %v, want nil", env.Data)
	}
}

// --- Iface kindof field ---

func TestKindof_IfaceField(t *testing.T) {
	src := `{"data":{"name":"Alice","role":"admin"}}`
	var env kindofEnvelopeIface
	if err := Unmarshal([]byte(src), &env); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if env.Data == nil {
		t.Fatal("Data is nil")
	}
	if env.Data.KindName() != "user" {
		t.Errorf("KindName() = %q, want %q", env.Data.KindName(), "user")
	}
	u, ok := env.Data.(kindofUser)
	if !ok {
		t.Fatalf("Data = %T, want kindofUser", env.Data)
	}
	if u.Name != "Alice" || u.Role != "admin" {
		t.Errorf("Data = %+v, want {Alice admin}", u)
	}
}

func TestKindof_IfaceFieldNull(t *testing.T) {
	src := `{"data":null}`
	var env kindofEnvelopeIface
	if err := Unmarshal([]byte(src), &env); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if env.Data != nil {
		t.Errorf("Data = %v, want nil", env.Data)
	}
}

// --- Repeated parse: parser reusable across calls ---

func TestKindof_RepeatedParse(t *testing.T) {
	srcs := []string{
		`{"data":true}`,
		`{"data":42}`,
		`{"data":"hi"}`,
		`{"data":{"name":"Alice","role":"admin"}}`,
		`{"data":[{"name":"Bob","role":"user"}]}`,
	}
	for i, src := range srcs {
		var env kindofEnvelopeMixed
		if err := Unmarshal([]byte(src), &env); err != nil {
			t.Fatalf("iter %d Unmarshal: %v", i, err)
		}
		if env.Data == nil {
			t.Errorf("iter %d: Data is nil", i)
		}
	}
}

// --- Nested: kindof case that is itself a variant envelope ---

func TestKindof_NestedVariantHost(t *testing.T) {
	// kindofEnvelopeMixed's "object" case is kindofUser (a plain struct).
	// For a true nested-variant test, register a kindof whose object case is
	// a variant envelope. Use a fresh host type so the registry entry doesn't
	// collide with kindofEnvelopeMixed.
	// (Deferred: this requires a dedicated host type + init registration.
	// The walker's recursion handles it automatically via UnmarshalValueInto
	// dispatching back into walkStruct which routes variant fields. Skip
	// explicit test for now; the recursive case is covered by variant tests.)
	t.Skip("nested kindof-within-variant covered by walker recursion; explicit test deferred")
}

// --- Multiple kindof fields per struct ---
//
// kindof fields are independent (no disc_seen pairing), so a struct may carry
// several. Each field gets its own BindKindofTable and its own poly_stack
// entry at parse time; the close loop drains them LIFO.

// kindofDualFieldFull is a host with two kindof fields sharing one descriptor
// that covers all 5 kinds. This lets each field dispatch to any kind
// independently.
type kindofDualFieldFull struct {
	Primary   any `json:"primary" vjson:"kindof"`
	Secondary any `json:"secondary" vjson:"kindof"`
}

func init() {
	vbind.DefineKindofCases[kindofDualFieldFull, struct {
		bool   bool
		number float64
		string string
		array  []kindofUser
		object kindofUser
	}]()
}

func TestKindof_MultipleFields_BothScalar(t *testing.T) {
	// Both fields scalar (inline path, state==2 each). Close loop pops both.
	src := `{"primary":true,"secondary":42.5}`
	var env kindofDualFieldFull
	if err := Unmarshal([]byte(src), &env); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	b, ok := env.Primary.(bool)
	if !ok || !b {
		t.Errorf("Primary = %v, want true", env.Primary)
	}
	n, ok := env.Secondary.(float64)
	if !ok || n != 42.5 {
		t.Errorf("Secondary = %v, want 42.5", env.Secondary)
	}
}

func TestKindof_MultipleFields_BothObject(t *testing.T) {
	// Both fields object (inline path, struct descend, close loop pops both
	// after each struct case closes back to the host).
	src := `{"primary":{"name":"Alice","role":"admin"},"secondary":{"name":"Bob","role":"user"}}`
	var env kindofDualFieldFull
	if err := Unmarshal([]byte(src), &env); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	p, ok := env.Primary.(kindofUser)
	if !ok || p.Name != "Alice" {
		t.Errorf("Primary = %+v, want {Alice}", env.Primary)
	}
	s, ok := env.Secondary.(kindofUser)
	if !ok || s.Name != "Bob" {
		t.Errorf("Secondary = %+v, want {Bob}", env.Secondary)
	}
}

func TestKindof_MultipleFields_MixedKinds(t *testing.T) {
	// One scalar, one object, and one array exercise the close loop across
	// different kind cases on the same struct.
	src := `{"primary":true,"secondary":{"name":"Alice","role":"admin"}}`
	var env kindofDualFieldFull
	if err := Unmarshal([]byte(src), &env); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if b, ok := env.Primary.(bool); !ok || !b {
		t.Errorf("Primary = %v, want true", env.Primary)
	}
	if u, ok := env.Secondary.(kindofUser); !ok || u.Name != "Alice" {
		t.Errorf("Secondary = %+v, want {Alice}", env.Secondary)
	}
}

func TestKindof_MultipleFields_BothNull(t *testing.T) {
	src := `{"primary":null,"secondary":null}`
	var env kindofDualFieldFull
	if err := Unmarshal([]byte(src), &env); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if env.Primary != nil || env.Secondary != nil {
		t.Errorf("Primary=%v Secondary=%v, want both nil", env.Primary, env.Secondary)
	}
}

func TestKindof_MultipleFields_RepeatedParse(t *testing.T) {
	// Reusable parser across multiple multi-field parses.
	srcs := []string{
		`{"primary":true,"secondary":false}`,
		`{"primary":{"name":"A","role":"x"},"secondary":{"name":"B","role":"y"}}`,
		`{"primary":1.5,"secondary":"hi"}`,
	}
	for i, src := range srcs {
		var env kindofDualFieldFull
		if err := Unmarshal([]byte(src), &env); err != nil {
			t.Fatalf("iter %d Unmarshal: %v", i, err)
		}
		if env.Primary == nil || env.Secondary == nil {
			t.Errorf("iter %d: Primary=%v Secondary=%v", i, env.Primary, env.Secondary)
		}
	}
}
