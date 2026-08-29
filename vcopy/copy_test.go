package vcopy

import (
	"bytes"
	"encoding/json"
	"reflect"
	"testing"
	"unsafe"

	"github.com/velox-io/json/gort"
)

// ---- Test fixtures ----

type Inner struct {
	A int32
	B string
	C float64
}

type Nested struct {
	Name     string
	Inner    Inner
	Inners   []Inner
	InnerP   *Inner
	InnerM   map[string]Inner
	InnerArr [3]Inner
}

func mkFixture() Nested {
	return Nested{
		Name:  "root",
		Inner: Inner{A: 1, B: "a", C: 1.5},
		Inners: []Inner{
			{A: 10, B: "ten", C: 10.0},
			{A: 20, B: "twenty", C: 20.0},
			{A: 30, B: "thirty", C: 30.0},
		},
		InnerP: &Inner{A: 99, B: "ptr", C: 99.99},
		InnerM: map[string]Inner{
			"x": {A: 100, B: "X", C: 100.0},
			"y": {A: 200, B: "Y", C: 200.0},
		},
		InnerArr: [3]Inner{
			{A: 1, B: "arr1", C: 1.0},
			{A: 2, B: "arr2", C: 2.0},
			{A: 3, B: "arr3", C: 3.0},
		},
	}
}

// ---- Correctness ----

func TestDeepCopy_Equivalent(t *testing.T) {
	src := mkFixture()
	dst, err := DeepCopy(src)
	if err != nil {
		t.Fatalf("DeepCopy: %v", err)
	}

	// Value equality via JSON (handles slices/maps/pointers uniformly).
	sb, _ := json.Marshal(src)
	db, _ := json.Marshal(dst)
	if !bytes.Equal(sb, db) {
		t.Fatalf("DeepCopy result differs from source\nsrc=%s\ndst=%s", sb, db)
	}
}

func TestDeepCopy_IsolatesSlicesAndMaps(t *testing.T) {
	src := mkFixture()
	dst, err := DeepCopy(src)
	if err != nil {
		t.Fatalf("DeepCopy: %v", err)
	}

	// Mutate dst; src must be unaffected.
	dst.Inners[0].A = 777
	dst.Inners[0].B = "mut"
	mx := dst.InnerM["x"]
	mx.A = 888
	dst.InnerM["x"] = mx
	dst.InnerP.A = 999
	dst.InnerArr[0].A = 111
	dst.Inner.B = "modified"

	if src.Inners[0].A != 10 || src.Inners[0].B != "ten" {
		t.Errorf("src.Inners[0] mutated: %+v", src.Inners[0])
	}
	if src.InnerM["x"].A != 100 {
		t.Errorf("src.InnerM[x] mutated: %+v", src.InnerM["x"])
	}
	if src.InnerP.A != 99 {
		t.Errorf("src.InnerP mutated: %+v", *src.InnerP)
	}
	if src.InnerArr[0].A != 1 {
		t.Errorf("src.InnerArr[0] mutated: %+v", src.InnerArr[0])
	}
	if src.Inner.B != "a" {
		t.Errorf("src.Inner mutated: %+v", src.Inner)
	}
}

func TestDeepCopy_NilPointerAndEmptySlice(t *testing.T) {
	type T struct {
		P    *Inner
		S    []Inner
		M    map[string]Inner
		Str  string
		Str2 string // empty
	}
	src := T{P: nil, S: []Inner{}, M: nil, Str: "x", Str2: ""}
	dst, err := DeepCopy(src)
	if err != nil {
		t.Fatalf("DeepCopy: %v", err)
	}
	if dst.P != nil {
		t.Errorf("nil pointer not preserved: %v", dst.P)
	}
	if dst.M != nil {
		t.Errorf("nil map not preserved: %v", dst.M)
	}
	if len(dst.S) != 0 {
		t.Errorf("empty slice not preserved: %v", dst.S)
	}
	// Empty string must not panic on string-deepcopy path.
	if dst.Str != "x" || dst.Str2 != "" {
		t.Errorf("strings not preserved: %+v", dst)
	}
}

// ---- Cycle handling ----

// cyclicNode builds a doubly-linked list, which is the simplest non-trivial
// cycle in Go (next points forward, prev points back).
type cyclicNode struct {
	V    int
	Next *cyclicNode
	Prev *cyclicNode
}

func mkCyclicRing(n int) *cyclicNode {
	if n == 0 {
		return nil
	}
	nodes := make([]*cyclicNode, n)
	for i := range nodes {
		nodes[i] = &cyclicNode{V: i}
	}
	for i := range nodes {
		nodes[i].Next = nodes[(i+1)%n]
		nodes[i].Prev = nodes[(i-1+n)%n]
	}
	return nodes[0]
}

func TestDeepCopy_CyclicPointer(t *testing.T) {
	src := mkCyclicRing(5)
	dst, err := DeepCopy(src)
	if err != nil {
		t.Fatalf("DeepCopy cyclic: %v", err)
	}
	if dst == nil {
		t.Fatal("dst is nil")
	}

	// Walk forward 10 steps (5 elements, twice around). Must terminate and
	// return to start, proving the cycle is preserved rather than flattened.
	cur := dst
	for i := 0; i < 10; i++ {
		cur = cur.Next
		if cur == nil {
			t.Fatalf("nil at step %d: cycle broken", i)
		}
	}
	if cur != dst {
		t.Errorf("forward walk did not return to start")
	}

	// Walk backward 10 steps.
	cur = dst
	for i := 0; i < 10; i++ {
		cur = cur.Prev
		if cur == nil {
			t.Fatalf("nil at step %d: cycle broken", i)
		}
	}
	if cur != dst {
		t.Errorf("backward walk did not return to start")
	}

	// Identity: dst.Next.Prev must BE dst (same pointer), not a clone.
	if dst.Next.Prev != dst {
		t.Errorf("cycle identity lost: dst.Next.Prev != dst")
	}
}

// selfRef is a single-node self-cycle: a struct holding a pointer to itself.
type selfRef struct {
	V int
	P *selfRef
}

func TestDeepCopy_SelfReferential(t *testing.T) {
	src := &selfRef{V: 42}
	src.P = src // self-cycle

	dst, err := DeepCopy(src)
	if err != nil {
		t.Fatalf("DeepCopy self-ref: %v", err)
	}
	if dst.V != 42 {
		t.Errorf("V not copied: %d", dst.V)
	}
	if dst.P != dst {
		t.Errorf("self-cycle not preserved: dst.P=%p dst=%p", dst.P, dst)
	}
}

// sharedTarget verifies that a single source node referenced by two parents
// is cloned exactly once in the destination, preserving the diamond shape.
type sharedParent struct {
	V    int
	Leaf *Inner
}

func TestDeepCopy_SharedPointerNotDuplicated(t *testing.T) {
	leaf := &Inner{A: 7, B: "leaf", C: 7.7}
	src := &sharedParent{
		V:    1,
		Leaf: leaf,
	}
	src2 := &sharedParent{
		V:    2,
		Leaf: leaf, // same leaf
	}
	// Wrap in a parent struct to deep-copy together.
	type pair struct {
		A *sharedParent
		B *sharedParent
	}
	p := pair{A: src, B: src2}

	dst, err := DeepCopy(p)
	if err != nil {
		t.Fatalf("DeepCopy shared: %v", err)
	}
	if dst.A.Leaf != dst.B.Leaf {
		t.Errorf("shared pointer duplicated: A.Leaf=%p B.Leaf=%p (should match)",
			dst.A.Leaf, dst.B.Leaf)
	}
	if dst.A.Leaf == leaf {
		t.Errorf("leaf not deep-copied: dst aliases src")
	}
}

// ---- Interface (any / non-empty) ----

// anyHolder exercises KindAny: a struct with an `any` field holding values
// of several dynamic kinds.
type anyHolder struct {
	N   int
	Box any
}

func TestDeepCopy_AnyScalar(t *testing.T) {
	src := anyHolder{N: 1, Box: int64(42)}
	dst, err := DeepCopy(src)
	if err != nil {
		t.Fatalf("DeepCopy any scalar: %v", err)
	}
	if g, ok := dst.Box.(int64); !ok || g != 42 {
		t.Errorf("any int64 not preserved: %+v", dst.Box)
	}
}

func TestDeepCopy_AnyString(t *testing.T) {
	src := anyHolder{N: 1, Box: "hello"}
	dst, err := DeepCopy(src)
	if err != nil {
		t.Fatalf("DeepCopy any string: %v", err)
	}
	if g, ok := dst.Box.(string); !ok || g != "hello" {
		t.Errorf("any string not preserved: %+v", dst.Box)
	}
	// String must be deep-copied: dst backing array must not alias src.
	srcStr := src.Box.(string)
	dstStr := dst.Box.(string)
	if len(srcStr) > 0 {
		sh1 := (*[2]unsafe.Pointer)(unsafe.Pointer(&srcStr))
		sh2 := (*[2]unsafe.Pointer)(unsafe.Pointer(&dstStr))
		if sh1[0] == sh2[0] {
			t.Errorf("any string not deep-copied: aliasing backing array")
		}
	}
}

func TestDeepCopy_AnyStruct(t *testing.T) {
	src := anyHolder{N: 1, Box: Inner{A: 5, B: "x", C: 5.5}}
	dst, err := DeepCopy(src)
	if err != nil {
		t.Fatalf("DeepCopy any struct: %v", err)
	}
	g, ok := dst.Box.(Inner)
	if !ok {
		t.Fatalf("any struct type not preserved: %T", dst.Box)
	}
	if g.A != 5 || g.B != "x" || g.C != 5.5 {
		t.Errorf("any struct value not preserved: %+v", g)
	}
}

func TestDeepCopy_AnySlice(t *testing.T) {
	src := anyHolder{N: 1, Box: []Inner{{A: 1, B: "a"}, {A: 2, B: "b"}}}
	dst, err := DeepCopy(src)
	if err != nil {
		t.Fatalf("DeepCopy any slice: %v", err)
	}
	g, ok := dst.Box.([]Inner)
	if !ok {
		t.Fatalf("any slice type not preserved: %T", dst.Box)
	}
	if len(g) != 2 || g[0].A != 1 || g[1].B != "b" {
		t.Errorf("any slice not preserved: %+v", g)
	}
	// Mutate dst; src must be unaffected (proves deep copy).
	g[0].A = 999
	if src.Box.([]Inner)[0].A != 1 {
		t.Errorf("any slice aliased src after mutation")
	}
}

func TestDeepCopy_AnyPointer(t *testing.T) {
	inner := &Inner{A: 7, B: "p", C: 7.7}
	src := anyHolder{N: 1, Box: inner}
	dst, err := DeepCopy(src)
	if err != nil {
		t.Fatalf("DeepCopy any pointer: %v", err)
	}
	g, ok := dst.Box.(*Inner)
	if !ok {
		t.Fatalf("any pointer type not preserved: %T", dst.Box)
	}
	if g.A != 7 || g.B != "p" {
		t.Errorf("any pointer value not preserved: %+v", *g)
	}
	if g == inner {
		t.Errorf("any pointer aliases src")
	}
	// Mutate dst pointer target; src must be unaffected.
	g.A = 888
	if inner.A != 7 {
		t.Errorf("any pointer target aliased src")
	}
}

func TestDeepCopy_AnyNil(t *testing.T) {
	src := anyHolder{N: 1, Box: nil}
	dst, err := DeepCopy(src)
	if err != nil {
		t.Fatalf("DeepCopy any nil: %v", err)
	}
	if dst.Box != nil {
		t.Errorf("any nil not preserved: %+v", dst.Box)
	}
}

// TestDeepCopy_AnyCyclicPointer: an interface holding a pointer that points
// back to the holder. This is the worst case: cycle through interface.
type cyclicHolder struct {
	V    int
	Next any // holds *cyclicHolder
}

func TestDeepCopy_AnyCyclicPointer(t *testing.T) {
	src := &cyclicHolder{V: 1}
	src.Next = &cyclicHolder{V: 2}
	src.Next.(*cyclicHolder).Next = src // back edge through interface

	dst, err := DeepCopy(src)
	if err != nil {
		t.Fatalf("DeepCopy any cyclic: %v", err)
	}
	if dst.V != 1 {
		t.Errorf("dst.V: %d", dst.V)
	}
	next, ok := dst.Next.(*cyclicHolder)
	if !ok {
		t.Fatalf("dst.Next type: %T", dst.Next)
	}
	if next.V != 2 {
		t.Errorf("dst.Next.V: %d", next.V)
	}
	// The back edge: next.Next must point back to dst.
	if next.Next.(*cyclicHolder) != dst {
		t.Errorf("cycle through interface not preserved: next.Next=%p dst=%p",
			next.Next.(*cyclicHolder), dst)
	}
}

// TestDeepCopy_NonEmptyInterface: a non-empty interface (with methods).
type stringer interface {
	String() string
}

type stringerImpl struct {
	V int
	S string
}

func (s stringerImpl) String() string { return s.S }

type ifaceHolder struct {
	Box stringer
}

func TestDeepCopy_NonEmptyInterface(t *testing.T) {
	src := ifaceHolder{Box: stringerImpl{V: 3, S: "z"}}
	dst, err := DeepCopy(src)
	if err != nil {
		t.Fatalf("DeepCopy non-empty iface: %v", err)
	}
	g, ok := dst.Box.(stringerImpl)
	if !ok {
		t.Fatalf("type not preserved: %T", dst.Box)
	}
	if g.V != 3 || g.S != "z" {
		t.Errorf("value not preserved: %+v", g)
	}
}

func TestDeepCopy_NonEmptyInterfaceNil(t *testing.T) {
	src := ifaceHolder{Box: nil}
	dst, err := DeepCopy(src)
	if err != nil {
		t.Fatalf("DeepCopy nil non-empty iface: %v", err)
	}
	if dst.Box != nil {
		t.Errorf("nil not preserved: %+v", dst.Box)
	}
}

// ---- Benchmarks ----

// BenchmarkReflectDeepCopy is the reflect-driven baseline. There is no
// reflect.DeepCopy in the stdlib; this hand-rolled recursive reflect.Value
// walk is what a generic library would do.
func BenchmarkReflectDeepCopy(b *testing.B) {
	src := mkFixture()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = reflectDeepCopy(reflect.ValueOf(src)).Interface().(Nested)
	}
}

func reflectDeepCopy(v reflect.Value) reflect.Value {
	switch v.Kind() {
	case reflect.Pointer:
		if v.IsNil() {
			return reflect.Zero(v.Type())
		}
		dst := reflect.New(v.Type().Elem())
		dst.Elem().Set(reflectDeepCopy(v.Elem()))
		return dst
	case reflect.Slice:
		if v.IsNil() {
			return reflect.Zero(v.Type())
		}
		dst := reflect.MakeSlice(v.Type(), v.Len(), v.Len())
		for i := 0; i < v.Len(); i++ {
			dst.Index(i).Set(reflectDeepCopy(v.Index(i)))
		}
		return dst
	case reflect.Map:
		if v.IsNil() {
			return reflect.Zero(v.Type())
		}
		dst := reflect.MakeMapWithSize(v.Type(), v.Len())
		it := v.MapRange()
		for it.Next() {
			dst.SetMapIndex(reflectDeepCopy(it.Key()), reflectDeepCopy(it.Value()))
		}
		return dst
	case reflect.Struct:
		dst := reflect.New(v.Type()).Elem()
		for i := 0; i < v.NumField(); i++ {
			dst.Field(i).Set(reflectDeepCopy(v.Field(i)))
		}
		return dst
	case reflect.Array:
		dst := reflect.New(v.Type()).Elem()
		for i := 0; i < v.Len(); i++ {
			dst.Index(i).Set(reflectDeepCopy(v.Index(i)))
		}
		return dst
	case reflect.String:
		s := v.String()
		if len(s) == 0 {
			return v
		}
		b := make([]byte, len(s))
		copy(b, s)
		return reflect.ValueOf(unsafe.String(&b[0], len(b)))
	case reflect.Interface:
		if v.IsNil() {
			return v
		}
		// Unbox, deep-copy the concrete value, rebox.
		concrete := v.Elem()
		copied := reflectDeepCopy(concrete)
		// Box back into an any of the static interface type.
		return reflect.ValueOf(copied.Interface())
	default:
		// Scalars: return by value.
		return v
	}
}

// BenchmarkJSONRoundtrip is the json.Marshal+Unmarshal baseline. It is the
// most expensive but requires zero custom code.
func BenchmarkJSONRoundtrip(b *testing.B) {
	src := mkFixture()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		buf, _ := json.Marshal(src)
		var dst Nested
		_ = json.Unmarshal(buf, &dst)
	}
}

// BenchmarkVcopyDeepCopy is the vcopy fast path.
func BenchmarkVcopyDeepCopy(b *testing.B) {
	src := mkFixture()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = DeepCopy(src)
	}
}

// BenchmarkVcopyCopyInto isolates the cost of the copy itself, without the
// generic-result boxing in DeepCopy[T].
func BenchmarkVcopyCopyInto(b *testing.B) {
	src := mkFixture()
	var dst Nested
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = CopyInto(src, &dst)
	}
}

// BenchmarkGortMemmoveScalar is a lower bound: how fast a single typed
// memmove of the same payload size runs. This represents the theoretical
// floor for any deep copy of Nested.
func BenchmarkGortMemmoveScalar(b *testing.B) {
	src := mkFixture()
	var dst Nested
	t := reflect.TypeFor[Nested]()
	tp := gort.TypePtr(t)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		gort.TypedMemmove(tp, unsafe.Pointer(&dst), unsafe.Pointer(&src))
	}
}

// BenchmarkVcopyMapStringAny is the JSON-parsed-output caching scenario:
// a map[string]any of the kind vdec produces when decoding into interface{}.
// Every value goes through copyInterface, so this isolates the reflect-free
// interface path.
func BenchmarkVcopyMapStringAny(b *testing.B) {
	src := map[string]any{
		"id":     int64(12345),
		"name":   "widget",
		"active": true,
		"price":  19.95,
		"tags":   []any{"a", "b", "c"},
		"meta": map[string]any{
			"created": "2026-01-01",
			"count":   int64(42),
		},
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = DeepCopy(src)
	}
}

// BenchmarkReflectMapStringAny is the reflect-driven baseline for the same
// map[string]any workload.
func BenchmarkReflectMapStringAny(b *testing.B) {
	src := map[string]any{
		"id":     int64(12345),
		"name":   "widget",
		"active": true,
		"price":  19.95,
		"tags":   []any{"a", "b", "c"},
		"meta": map[string]any{
			"created": "2026-01-01",
			"count":   int64(42),
		},
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = reflectDeepCopy(reflect.ValueOf(src)).Interface().(map[string]any)
	}
}

// ---- Promotion across an embedded pointer ----

// A promoted field's offset is relative to the pointee its hops reach, and the
// pointer word itself is not in the field list, so nothing else would copy it.
// Copying has to walk the hops and allocate on the destination, or the copy
// would either miss the field or alias the source's pointee.

type CopyPtrLeaf struct {
	D string
}

type CopyPtrMid struct {
	*CopyPtrLeaf
	M int
}

type CopyPtrHost struct {
	*CopyPtrMid
	Top string
}

func TestDeepCopy_PromotedAcrossPointer(t *testing.T) {
	src := CopyPtrHost{
		CopyPtrMid: &CopyPtrMid{CopyPtrLeaf: &CopyPtrLeaf{D: "deep"}, M: 5},
		Top:        "t",
	}
	dst, err := DeepCopy(src)
	if err != nil {
		t.Fatalf("DeepCopy: %v", err)
	}

	if dst.Top != "t" || dst.M != 5 || dst.D != "deep" {
		t.Fatalf("promoted fields not copied: %+v", dst)
	}
	// Deep means the destination owns its own pointees, so mutating the source
	// through them must not be observable in the copy.
	if dst.CopyPtrMid == src.CopyPtrMid {
		t.Error("destination aliases the source's mid pointee")
	}
	if dst.CopyPtrLeaf == src.CopyPtrLeaf {
		t.Error("destination aliases the source's leaf pointee")
	}
	src.D = "mutated"
	src.M = 99
	if dst.D != "deep" || dst.M != 5 {
		t.Errorf("copy observed a source mutation: %+v", dst)
	}
}

func TestDeepCopy_PromotedAcrossNilPointer(t *testing.T) {
	// A nil hop has no value to copy. Allocating one would invent data the
	// source never had, so the destination stays nil.
	src := CopyPtrHost{Top: "t"}
	dst, err := DeepCopy(src)
	if err != nil {
		t.Fatalf("DeepCopy: %v", err)
	}
	if dst.Top != "t" {
		t.Errorf("Top = %q, want t", dst.Top)
	}
	if dst.CopyPtrMid != nil {
		t.Errorf("nil hop was allocated: %+v", dst.CopyPtrMid)
	}

	// Partially built: the outer hop exists, the inner does not.
	src2 := CopyPtrHost{CopyPtrMid: &CopyPtrMid{M: 7}, Top: "t"}
	dst2, err := DeepCopy(src2)
	if err != nil {
		t.Fatalf("DeepCopy: %v", err)
	}
	if dst2.CopyPtrMid == nil || dst2.M != 7 {
		t.Fatalf("outer hop not copied: %+v", dst2)
	}
	if dst2.CopyPtrLeaf != nil {
		t.Errorf("nil inner hop was allocated: %+v", dst2.CopyPtrLeaf)
	}
}
