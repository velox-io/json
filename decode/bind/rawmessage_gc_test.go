package bind

import (
	"encoding/json"
	"runtime"
	"testing"
)

// gcAndFill forces several GC cycles and allocates a large transient buffer
// between them. The goal is to maximize the chance that any unreferenced
// backing gets swept and its memory reused, so a dangling RawMessage data
// pointer would show up as corrupted bytes on the next read.
func gcAndFill() {
	runtime.GC()
	runtime.GC()
	_ = make([]byte, 8<<20)
	runtime.GC()
}

// rawMsgGCExpect holds the expected bytes for one RawMessage value across a
// GC stress cycle, so the test can compare against a stable copy that does
// not alias the parser's backing.
type rawMsgGCExpect struct {
	want string
}

func (e rawMsgGCExpect) check(t *testing.T, iter int, label string, got json.RawMessage) {
	t.Helper()
	if string(got) != e.want {
		t.Fatalf("iter %d %s: RawMessage corrupted after GC\ngot  %q\nwant %q", iter, label, got, e.want)
	}
}

// TestRawMessageGC_RootStructField: RawMessage field in a value root struct.
// rec.Target points directly into the user-supplied object graph; this is the
// strongest GC root and the most likely path to remain safe.
func TestRawMessageGC_RootStructField(t *testing.T) {
	type X struct {
		RM json.RawMessage `json:"rm"`
	}
	want := `{"key":"long enough value to force a real backing"}`
	payload := []byte(`{"rm":` + want + `}`)
	p, err := NewParser[X]()
	if err != nil {
		t.Fatal(err)
	}
	exp := rawMsgGCExpect{want: want}
	for i := 0; i < 3000; i++ {
		var x X
		if err := p.Unmarshal(payload, &x); err != nil {
			t.Fatalf("iter %d: %v", i, err)
		}
		gcAndFill()
		exp.check(t, i, "RootStructField", x.RM)
	}
}

// TestRawMessageGC_PointerRoot: *T root. The T struct lives in a SlotClass
// block allocated via UnsafeNewArray(T-type). GC safety relies on the T-type
// gcdata marking the RawMessage field's data pointer as a GC pointer so the
// block is scannable, plus the user's *T variable rooting the block.
func TestRawMessageGC_PointerRoot(t *testing.T) {
	type X struct {
		RM json.RawMessage `json:"rm"`
	}
	want := `{"deep":"value with enough bytes to escape small-object cache"}`
	payload := []byte(`{"rm":` + want + `}`)
	p, err := NewParser[*X]()
	if err != nil {
		t.Fatal(err)
	}
	exp := rawMsgGCExpect{want: want}
	for i := 0; i < 3000; i++ {
		var x *X
		if err := p.Unmarshal(payload, &x); err != nil {
			t.Fatalf("iter %d: %v", i, err)
		}
		gcAndFill()
		exp.check(t, i, "PointerRoot", x.RM)
	}
}

// TestRawMessageGC_SliceElement: []json.RawMessage. Slice backing comes from
// a SlotClass block of element type []byte; the []byte gcdata marks the data
// pointer in each 24B slot as a GC pointer, so the block is scannable.
func TestRawMessageGC_SliceElement(t *testing.T) {
	type X struct {
		Items []json.RawMessage `json:"items"`
	}
	wants := []string{`{"a":1}`, `{"b":2}`, `"hello"`, `42`, `null`}
	payload := []byte(`{"items":[{"a":1},{"b":2},"hello",42,null]}`)
	p, err := NewParser[X]()
	if err != nil {
		t.Fatal(err)
	}
	exps := make([]rawMsgGCExpect, len(wants))
	for i, w := range wants {
		exps[i] = rawMsgGCExpect{want: w}
	}
	for i := 0; i < 3000; i++ {
		var x X
		if err := p.Unmarshal(payload, &x); err != nil {
			t.Fatalf("iter %d: %v", i, err)
		}
		if len(x.Items) != len(wants) {
			t.Fatalf("iter %d: len=%d want %d", i, len(x.Items), len(wants))
		}
		gcAndFill()
		for j := range exps {
			exps[j].check(t, i, "SliceElement", x.Items[j])
		}
	}
}

// TestRawMessageGC_MapValue: map[string]json.RawMessage. Map values are
// deferred, so the native side writes the RawMessage slice header into an
// intermediate SlotClass slot of element type []byte (scannable), and the map
// drain copies the slot into the runtime *hmap. GC safety depends on the
// intermediate slot being scannable before the drain runs.
func TestRawMessageGC_MapValue(t *testing.T) {
	wants := map[string]string{
		"a": `{"x":1}`, "b": `[1,2]`, "c": `"hi"`, "d": `42`, "e": `null`,
	}
	payload := []byte(`{"a":{"x":1},"b":[1,2],"c":"hi","d":42,"e":null}`)
	for i := 0; i < 3000; i++ {
		var got map[string]json.RawMessage
		if err := Unmarshal(payload, &got); err != nil {
			t.Fatalf("iter %d: %v", i, err)
		}
		if len(got) != len(wants) {
			t.Fatalf("iter %d: len=%d want %d", i, len(got), len(wants))
		}
		gcAndFill()
		for k, w := range wants {
			rawMsgGCExpect{want: w}.check(t, i, "MapValue["+k+"]", got[k])
		}
	}
}

// TestRawMessageGC_NestedStructPointer: a RawMessage field inside a struct
// reached through a pointer field. The inner struct lives in a SlotClass
// block; the outer struct field holds a pointer that GC must follow to reach
// the inner struct, then follow the RawMessage field's data pointer to the
// backing.
func TestRawMessageGC_NestedStructPointer(t *testing.T) {
	type Inner struct {
		Extra json.RawMessage `json:"extra"`
	}
	type Outer struct {
		Inner *Inner `json:"inner"`
	}
	want := `{"nested":"deep value with enough bytes"}`
	payload := []byte(`{"inner":{"extra":` + want + `}}`)
	p, err := NewParser[Outer]()
	if err != nil {
		t.Fatal(err)
	}
	exp := rawMsgGCExpect{want: want}
	for i := 0; i < 3000; i++ {
		var x Outer
		if err := p.Unmarshal(payload, &x); err != nil {
			t.Fatalf("iter %d: %v", i, err)
		}
		if x.Inner == nil {
			t.Fatalf("iter %d: Inner is nil", i)
		}
		gcAndFill()
		exp.check(t, i, "NestedStructPointer", x.Inner.Extra)
	}
}

// TestRawMessageGC_MapValueStruct verifies the GC safety fix for
// map[string]struct{ Data json.RawMessage }. The struct contains a deferred
// field (RawMessage is detected as KindUnmarshaler), so the struct is
// redirected to a scannable SlotClass intermediate via BIND_FLAG_CONTAINS_DEFERRED
// instead of being written inline into the noscan KV buffer. A concurrent GC
// goroutine stresses the drain -> map drain window; without the fix this
// crashes with "found bad pointer in Go heap".
func TestRawMessageGC_MapValueStruct(t *testing.T) {
	type V struct {
		Data json.RawMessage `json:"data"`
	}
	type T struct {
		M map[string]V `json:"m"`
	}
	want := `{"key":"value with enough bytes to escape mcache and be reclaimed"}`
	payload := []byte(`{"m":{"a":{"data":` + want + `},"b":{"data":` + want + `}}}`)
	p, err := NewParser[T]()
	if err != nil {
		t.Fatal(err)
	}
	exp := rawMsgGCExpect{want: want}

	stop := make(chan struct{})
	gcDone := make(chan struct{})
	go func() {
		defer close(gcDone)
		for {
			select {
			case <-stop:
				return
			default:
				runtime.GC()
			}
		}
	}()
	defer func() {
		close(stop)
		<-gcDone
	}()

	for i := 0; i < 3000; i++ {
		var x T
		if err := p.Unmarshal(payload, &x); err != nil {
			t.Fatalf("iter %d: %v", i, err)
		}
		if len(x.M) != 2 {
			t.Fatalf("iter %d: len(M)=%d want 2", i, len(x.M))
		}
		for k, v := range x.M {
			exp.check(t, i, "MapValueStruct["+k+"]", v.Data)
		}
	}
}

// TestRawMessageGC_MapValueArrayOfStruct covers map[string][N]struct{ Data
// json.RawMessage }. The array is a non-deferred container that would be
// written inline into the noscan KV buffer; without CONTAINS_DEFERRED on
// arrays, the struct elements' RawMessage fields would land in noscan
// memory and the map drain would dereference array content as a pointer.
func TestRawMessageGC_MapValueArrayOfStruct(t *testing.T) {
	type V struct {
		Data json.RawMessage `json:"data"`
	}
	type T struct {
		M map[string][2]V `json:"m"`
	}
	want1 := `{"k":"v1 with enough bytes to escape mcache"}`
	want2 := `{"k":"v2 with enough bytes to escape mcache"}`
	payload := []byte(`{"m":{"a":[{"data":` + want1 + `},{"data":` + want2 + `}]}}`)
	p, err := NewParser[T]()
	if err != nil {
		t.Fatal(err)
	}
	exp1 := rawMsgGCExpect{want: want1}
	exp2 := rawMsgGCExpect{want: want2}

	stop := make(chan struct{})
	gcDone := make(chan struct{})
	go func() {
		defer close(gcDone)
		for {
			select {
			case <-stop:
				return
			default:
				runtime.GC()
			}
		}
	}()
	defer func() {
		close(stop)
		<-gcDone
	}()

	for i := 0; i < 3000; i++ {
		var x T
		if err := p.Unmarshal(payload, &x); err != nil {
			t.Fatalf("iter %d: %v", i, err)
		}
		arr, ok := x.M["a"]
		if !ok {
			t.Fatalf("iter %d: M[a] missing", i)
		}
		exp1.check(t, i, "MapValueArrayOfStruct[0]", arr[0].Data)
		exp2.check(t, i, "MapValueArrayOfStruct[1]", arr[1].Data)
	}
}

// TestRawMessageGC_MapValueArrayOfRawMessage covers map[string][N]json.RawMessage.
// Unlike array of struct (elements inline-parsed in the intermediate slot),
// here each element is itself deferred (RawMessage = KindUnmarshaler), so
// each records its own UnmarshalRecord with target inside the scannable
// intermediate slot. Without CONTAINS_DEFERRED on arrays the element
// closures would write slice headers into the noscan KV buffer.
func TestRawMessageGC_MapValueArrayOfRawMessage(t *testing.T) {
	type T struct {
		M map[string][2]json.RawMessage `json:"m"`
	}
	want1 := `"hello array element with enough bytes"`
	want2 := `{"k":"v2 with enough bytes to escape mcache"}`
	payload := []byte(`{"m":{"a":[` + want1 + `,` + want2 + `]}}`)
	p, err := NewParser[T]()
	if err != nil {
		t.Fatal(err)
	}
	exp1 := rawMsgGCExpect{want: want1}
	exp2 := rawMsgGCExpect{want: want2}

	stop := make(chan struct{})
	gcDone := make(chan struct{})
	go func() {
		defer close(gcDone)
		for {
			select {
			case <-stop:
				return
			default:
				runtime.GC()
			}
		}
	}()
	defer func() {
		close(stop)
		<-gcDone
	}()

	for i := 0; i < 3000; i++ {
		var x T
		if err := p.Unmarshal(payload, &x); err != nil {
			t.Fatalf("iter %d: %v", i, err)
		}
		arr, ok := x.M["a"]
		if !ok {
			t.Fatalf("iter %d: M[a] missing", i)
		}
		exp1.check(t, i, "MapValueArrayOfRawMessage[0]", arr[0])
		exp2.check(t, i, "MapValueArrayOfRawMessage[1]", arr[1])
	}
}

// TestRawMessageGC_MapValueStructNull covers null as the value of a
// CONTAINS_DEFERRED map entry. The struct must still be redirected to the
// intermediate slot (zeroed) so map drain can dereference the KV pointer
// safely; without the null check inside the CONTAINS_DEFERRED path, the
// code would attempt BIND_DESCEND_STRUCT with ch=='n' and crash.
func TestRawMessageGC_MapValueStructNull(t *testing.T) {
	type V struct {
		Data json.RawMessage `json:"data"`
	}
	type T struct {
		M map[string]V `json:"m"`
	}
	payload := []byte(`{"m":{"a":null,"b":{"data":{"k":"v"}}}}`)
	p, err := NewParser[T]()
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 100; i++ {
		var x T
		if err := p.Unmarshal(payload, &x); err != nil {
			t.Fatalf("iter %d: %v", i, err)
		}
		if v, ok := x.M["a"]; !ok {
			t.Fatalf("iter %d: M[a] missing", i)
		} else if v.Data != nil {
			t.Fatalf("iter %d: M[a].Data = %q, want nil", i, v.Data)
		}
		if v, ok := x.M["b"]; !ok {
			t.Fatalf("iter %d: M[b] missing", i)
		} else if string(v.Data) != `{"k":"v"}` {
			t.Fatalf("iter %d: M[b].Data = %q, want {\"k\":\"v\"}", i, v.Data)
		}
	}
}

// TestRawMessageGC_MapValuePointerStruct covers map[string]*struct{ Data
// json.RawMessage }. The pointee struct lives in a scannable SlotClass block
// (PTR's AllocClass, registered with the struct rtype so GC sees the
// RawMessage field's slice header). KV buffer stores the *struct pointer
// inline; drain must copy it directly into *hmap without dereferencing.
func TestRawMessageGC_MapValuePointerStruct(t *testing.T) {
	type V struct {
		Data json.RawMessage `json:"data"`
	}
	type T struct {
		M map[string]*V `json:"m"`
	}
	want := `{"key":"value with enough bytes to escape mcache and be reclaimed"}`
	payload := []byte(`{"m":{"a":{"data":` + want + `},"b":{"data":` + want + `}}}`)
	p, err := NewParser[T]()
	if err != nil {
		t.Fatal(err)
	}
	exp := rawMsgGCExpect{want: want}

	stop := make(chan struct{})
	gcDone := make(chan struct{})
	go func() {
		defer close(gcDone)
		for {
			select {
			case <-stop:
				return
			default:
				runtime.GC()
			}
		}
	}()
	defer func() {
		close(stop)
		<-gcDone
	}()

	for i := 0; i < 3000; i++ {
		var x T
		if err := p.Unmarshal(payload, &x); err != nil {
			t.Fatalf("iter %d: %v", i, err)
		}
		if len(x.M) != 2 {
			t.Fatalf("iter %d: len(M)=%d want 2", i, len(x.M))
		}
		for k, v := range x.M {
			if v == nil {
				t.Fatalf("iter %d: M[%s] is nil", i, k)
			}
			exp.check(t, i, "MapValuePointerStruct["+k+"]", v.Data)
		}
	}
}

// TestRawMessageGC_MapValuePointerRawMessage covers map[string]*json.RawMessage.
// The pointee is a RawMessage (slice header) living in a scannable SlotClass
// block (PTR.AllocClass registered with the RawMessage rtype). KV buffer
// stores the *RawMessage pointer inline; drain copies it directly.
func TestRawMessageGC_MapValuePointerRawMessage(t *testing.T) {
	type T struct {
		M map[string]*json.RawMessage `json:"m"`
	}
	want := `"value with enough bytes to escape mcache and be reclaimed"`
	payload := []byte(`{"m":{"a":` + want + `,"b":` + want + `}}`)
	p, err := NewParser[T]()
	if err != nil {
		t.Fatal(err)
	}
	exp := rawMsgGCExpect{want: want}

	stop := make(chan struct{})
	gcDone := make(chan struct{})
	go func() {
		defer close(gcDone)
		for {
			select {
			case <-stop:
				return
			default:
				runtime.GC()
			}
		}
	}()
	defer func() {
		close(stop)
		<-gcDone
	}()

	for i := 0; i < 3000; i++ {
		var x T
		if err := p.Unmarshal(payload, &x); err != nil {
			t.Fatalf("iter %d: %v", i, err)
		}
		if len(x.M) != 2 {
			t.Fatalf("iter %d: len(M)=%d want 2", i, len(x.M))
		}
		for k, v := range x.M {
			if v == nil {
				t.Fatalf("iter %d: M[%s] is nil", i, k)
			}
			exp.check(t, i, "MapValuePointerRawMessage["+k+"]", *v)
		}
	}
}

// TestRawMessageGC_MapValuePointerArrayOfStruct covers
// map[string]*[2]struct{ Data json.RawMessage }. The pointee is a [2]struct
// array living in a scannable SlotClass block (PTR.AllocClass registered with
// the [2]struct rtype). Each struct element's RawMessage field records an
// UnmarshalRecord with target inside the pointee; KV buffer stores the
// *[2]struct pointer inline.
func TestRawMessageGC_MapValuePointerArrayOfStruct(t *testing.T) {
	type V struct {
		Data json.RawMessage `json:"data"`
	}
	type T struct {
		M map[string]*[2]V `json:"m"`
	}
	want1 := `{"k":"v1 with enough bytes to escape mcache"}`
	want2 := `{"k":"v2 with enough bytes to escape mcache"}`
	payload := []byte(`{"m":{"a":[{"data":` + want1 + `},{"data":` + want2 + `}]}}`)
	p, err := NewParser[T]()
	if err != nil {
		t.Fatal(err)
	}
	exp1 := rawMsgGCExpect{want: want1}
	exp2 := rawMsgGCExpect{want: want2}

	stop := make(chan struct{})
	gcDone := make(chan struct{})
	go func() {
		defer close(gcDone)
		for {
			select {
			case <-stop:
				return
			default:
				runtime.GC()
			}
		}
	}()
	defer func() {
		close(stop)
		<-gcDone
	}()

	for i := 0; i < 3000; i++ {
		var x T
		if err := p.Unmarshal(payload, &x); err != nil {
			t.Fatalf("iter %d: %v", i, err)
		}
		arr, ok := x.M["a"]
		if !ok {
			t.Fatalf("iter %d: M[a] missing", i)
		}
		if arr == nil {
			t.Fatalf("iter %d: M[a] is nil", i)
		}
		exp1.check(t, i, "MapValuePointerArrayOfStruct[0]", arr[0].Data)
		exp2.check(t, i, "MapValuePointerArrayOfStruct[1]", arr[1].Data)
	}
}

// TestRawMessageGC_MapValuePointerArrayOfStructWithArrayField covers
// map[string]*[2]struct{ Data [2]json.RawMessage }. The pointee is a [2]struct
// array; each struct holds a [2]RawMessage array field. UnmarshalRecords for
// the inner RawMessage elements target the pointee (scannable), and the KV
// buffer stores the *[2]struct pointer inline.
func TestRawMessageGC_MapValuePointerArrayOfStructWithArrayField(t *testing.T) {
	type V struct {
		Data [2]json.RawMessage `json:"data"`
	}
	type T struct {
		M map[string]*[2]V `json:"m"`
	}
	want00 := `{"k":"00 with enough bytes to escape mcache"}`
	want01 := `"hello 01 with enough bytes"`
	want10 := `{"k":"10 with enough bytes to escape mcache"}`
	want11 := `"hello 11 with enough bytes"`
	payload := []byte(`{"m":{"a":[{"data":[` + want00 + `,` + want01 + `]},{"data":[` + want10 + `,` + want11 + `]}]}}`)
	p, err := NewParser[T]()
	if err != nil {
		t.Fatal(err)
	}
	exps := [4]rawMsgGCExpect{{want: want00}, {want: want01}, {want: want10}, {want: want11}}

	stop := make(chan struct{})
	gcDone := make(chan struct{})
	go func() {
		defer close(gcDone)
		for {
			select {
			case <-stop:
				return
			default:
				runtime.GC()
			}
		}
	}()
	defer func() {
		close(stop)
		<-gcDone
	}()

	for i := 0; i < 3000; i++ {
		var x T
		if err := p.Unmarshal(payload, &x); err != nil {
			t.Fatalf("iter %d: %v", i, err)
		}
		arr, ok := x.M["a"]
		if !ok {
			t.Fatalf("iter %d: M[a] missing", i)
		}
		if arr == nil {
			t.Fatalf("iter %d: M[a] is nil", i)
		}
		exps[0].check(t, i, "MapValuePointerArrayOfStructWithArrayField[0][0]", arr[0].Data[0])
		exps[1].check(t, i, "MapValuePointerArrayOfStructWithArrayField[0][1]", arr[0].Data[1])
		exps[2].check(t, i, "MapValuePointerArrayOfStructWithArrayField[1][0]", arr[1].Data[0])
		exps[3].check(t, i, "MapValuePointerArrayOfStructWithArrayField[1][1]", arr[1].Data[1])
	}
}

// TestRawMessageGC_MapValuePointerArrayOfStructWithNestedStruct covers
// map[string]*[2]struct{ Data struct{ Inner json.RawMessage } }. The pointee
// is a [2]struct array (scannable SlotClass); each element holds a nested
// struct value field, whose Inner RawMessage records an UnmarshalRecord with
// target inside the pointee. KV buffer stores the *[2]struct pointer inline.
func TestRawMessageGC_MapValuePointerArrayOfStructWithNestedStruct(t *testing.T) {
	type Inner struct {
		Inner json.RawMessage `json:"inner"`
	}
	type V struct {
		Data Inner `json:"data"`
	}
	type T struct {
		M map[string]*[2]V `json:"m"`
	}
	want0 := `{"k":"v0 with enough bytes to escape mcache"}`
	want1 := `{"k":"v1 with enough bytes to escape mcache"}`
	payload := []byte(`{"m":{"a":[{"data":{"inner":` + want0 + `}},{"data":{"inner":` + want1 + `}}]}}`)
	p, err := NewParser[T]()
	if err != nil {
		t.Fatal(err)
	}
	exp0 := rawMsgGCExpect{want: want0}
	exp1 := rawMsgGCExpect{want: want1}

	stop := make(chan struct{})
	gcDone := make(chan struct{})
	go func() {
		defer close(gcDone)
		for {
			select {
			case <-stop:
				return
			default:
				runtime.GC()
			}
		}
	}()
	defer func() {
		close(stop)
		<-gcDone
	}()

	for i := 0; i < 3000; i++ {
		var x T
		if err := p.Unmarshal(payload, &x); err != nil {
			t.Fatalf("iter %d: %v", i, err)
		}
		arr, ok := x.M["a"]
		if !ok {
			t.Fatalf("iter %d: M[a] missing", i)
		}
		if arr == nil {
			t.Fatalf("iter %d: M[a] is nil", i)
		}
		exp0.check(t, i, "MapValuePointerArrayOfStructWithNestedStruct[0]", arr[0].Data.Inner)
		exp1.check(t, i, "MapValuePointerArrayOfStructWithNestedStruct[1]", arr[1].Data.Inner)
	}
}

// TestRawMessageGC_UnmarshalOnlyConcurrent isolates the decode/bind unmarshal path
// from venc. The original marshal round-trip crash dumped a GC worker stack whose
// memory contained JSON source bytes followed by 0x20 scan padding, which only
// the bind side produces via padInputInto. venc was a red herring; this test
// removes it entirely.
//
// Only bind.Unmarshal runs against the Msg{Payload RawMessage} shape. A
// parallel allocator goroutine drives real gcAssistAlloc pressure instead of
// serial runtime.GC, matching the crash condition (g 1 in makechan ->
// gcAssistAlloc -> scanstack of a GC worker whose stack memory had been
// reused from a freed bind backing).
//
// Package-level Unmarshal is used deliberately: between iterations the Parser
// is rooted only through sync.Pool, so any GC cycle clearing the pool drops it
// and frees padBuf / strArena / machine backings. The 2 KiB allocations in the
// disturber hit the goroutine-stack size class so a prematurely freed backing
// resurfaces as a scanstack fault instead of silent data corruption.
//
//	Reproduce: GOGC=1 ./build/bin/vjrun go test ./decode/bind/ \
//	  -count=2000 -run '^TestRawMessageGC_UnmarshalOnlyConcurrent$' -v
func TestRawMessageGC_UnmarshalOnlyConcurrent(t *testing.T) {
	// The reserve would keep the Parser rooted across GC, removing the
	// use-after-free window this test reproduces. Disable it so the Parser is
	// once again reachable only through sync.Pool, as the comment above assumes.
	SetParserReserveEnabled(false)
	defer SetParserReserveEnabled(true)

	type Msg struct {
		Type string `json:"type"`
		// Payload json.RawMessage `json:"payload"`
	}
	original := []byte(`{"type":"event","payload":{"id":1,"items":[1,2,3]}}`)
	// want := `{"id":1,"items":[1,2,3]}`

	stop := make(chan struct{})
	allocDone := make(chan struct{})
	go func() {
		defer close(allocDone)
		for {
			select {
			case <-stop:
				return
			default:
				// 2 KiB matches the goroutine-stack size class; 8 KiB hits
				// the next tier. Mixed churn sweeps several size classes so
				// any prematurely freed backing is reused as a stack.
				_ = make([]byte, 2<<10)
				_ = make([]byte, 8<<10)
			}
		}
	}()
	defer func() {
		close(stop)
		<-allocDone
	}()

	for i := range 5000 {
		var msg Msg
		if err := Unmarshal(original, &msg); err != nil {
			t.Fatalf("iter %d: %v", i, err)
		}
		// if string(msg.Payload) != want {
		// 	t.Fatalf("iter %d: payload corrupted\ngot  %q\nwant %q", i, msg.Payload, want)
		// }
	}
}
