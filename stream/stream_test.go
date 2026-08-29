//go:build !vdec

package stream_test

import (
	"testing"

	vjson "github.com/velox-io/json"
	"github.com/velox-io/json/stream"
)

type event struct {
	ID    string `json:"id"`
	Match bool   `json:"match"`
}

type host struct {
	Events stream.Stream[event] `json:"events"`
	Name   string               `json:"name"`
}

func TestStreamBasicIteration(t *testing.T) {
	var h host
	var ids []string

	h.Events.OnRead(func(s stream.Scope[event]) error {
		for it := range s.Iter() {
			if err := it.Decode(); err != nil {
				return err
			}
			ids = append(ids, it.Target().ID)
		}
		return nil
	})

	data := []byte(`{"events":[{"id":"e1","match":false},{"id":"e2","match":true}],"name":"alice"}`)
	if err := vjson.Unmarshal(data, &h); err != nil {
		t.Fatalf("Decode: %v", err)
	}
	if h.Name != "alice" {
		t.Errorf("Name = %q, want alice", h.Name)
	}
	want := []string{"e1", "e2"}
	if len(ids) != len(want) {
		t.Fatalf("ids = %v, want %v", ids, want)
	}
	for i := range want {
		if ids[i] != want[i] {
			t.Errorf("ids[%d] = %q, want %q", i, ids[i], want[i])
		}
	}
}

func TestStreamSkip(t *testing.T) {
	var h host
	var ids []string

	h.Events.OnRead(func(s stream.Scope[event]) error {
		for it := range s.Iter() {
			if err := it.Decode(); err != nil {
				return err
			}
			e := it.Target()
			if e.Match {
				// Skip the rest of the array by breaking the loop.
				// (Item.Skip is for skipping without consuming; once Decode
				// is called, use Go's native break to stop iteration.)
				break
			}
			ids = append(ids, e.ID)
		}
		return nil
	})

	data := []byte(`{"events":[{"id":"e1","match":false},{"id":"e2","match":true},{"id":"e3","match":false}],"name":"bob"}`)
	if err := vjson.Unmarshal(data, &h); err != nil {
		t.Fatalf("Decode: %v", err)
	}
	if h.Name != "bob" {
		t.Errorf("Name = %q, want bob", h.Name)
	}
	want := []string{"e1"}
	if len(ids) != len(want) {
		t.Fatalf("ids = %v, want %v (only e1 before break)", ids, want)
	}
	if ids[0] != want[0] {
		t.Errorf("ids[0] = %q, want %q", ids[0], want[0])
	}
}

func TestStreamItemSkip(t *testing.T) {
	var h host
	var ids []string

	h.Events.OnRead(func(s stream.Scope[event]) error {
		for it := range s.Iter() {
			// Skip without consuming: Item.Skip fast-forwards the rest
			// of the array.
			if err := it.Skip(); err != nil {
				return err
			}
			// Should not reach here after Skip.
			_ = it
		}
		return nil
	})

	data := []byte(`{"events":[{"id":"e1","match":false},{"id":"e2","match":true}],"name":"gail"}`)
	if err := vjson.Unmarshal(data, &h); err != nil {
		t.Fatalf("Decode: %v", err)
	}
	if h.Name != "gail" {
		t.Errorf("Name = %q, want gail", h.Name)
	}
	if len(ids) != 0 {
		t.Fatalf("ids = %v, want empty (all skipped)", ids)
	}
}

func TestStreamBreak(t *testing.T) {
	var h host
	var ids []string

	h.Events.OnRead(func(s stream.Scope[event]) error {
		for it := range s.Iter() {
			if err := it.Decode(); err != nil {
				return err
			}
			e := it.Target()
			if e.Match {
				break
			}
			ids = append(ids, e.ID)
		}
		return nil
	})

	data := []byte(`{"events":[{"id":"e1","match":false},{"id":"e2","match":true},{"id":"e3","match":false}],"name":"carol"}`)
	if err := vjson.Unmarshal(data, &h); err != nil {
		t.Fatalf("Decode: %v", err)
	}
	if h.Name != "carol" {
		t.Errorf("Name = %q, want carol", h.Name)
	}
	want := []string{"e1"}
	if len(ids) != len(want) {
		t.Fatalf("ids = %v, want %v", ids, want)
	}
}

func TestStreamEmpty(t *testing.T) {
	var h host
	activated := false

	h.Events.OnRead(func(s stream.Scope[event]) error {
		activated = true
		for range s.Iter() {
			t.Error("unexpected item in empty stream")
		}
		return nil
	})

	data := []byte(`{"events":[],"name":"dave"}`)
	if err := vjson.Unmarshal(data, &h); err != nil {
		t.Fatalf("Decode: %v", err)
	}
	if !activated {
		t.Error("handler not activated for empty array")
	}
	if h.Name != "dave" {
		t.Errorf("Name = %q, want dave", h.Name)
	}
}

func TestStreamNullField(t *testing.T) {
	var h host

	h.Events.OnRead(func(s stream.Scope[event]) error {
		for range s.Iter() {
		}
		return nil
	})

	data := []byte(`{"events":null,"name":"eve"}`)
	if err := vjson.Unmarshal(data, &h); err != nil {
		t.Fatalf("Decode: %v", err)
	}
	// null does not activate the handler (no array opened); the stream
	// field stays zero-valued. This matches encoding/json's null semantics
	// for a custom Unmarshaler that does not implement null specially.
	if h.Name != "eve" {
		t.Errorf("Name = %q, want eve", h.Name)
	}
}

func TestStreamNoHandler(t *testing.T) {
	var h host
	data := []byte(`{"events":[{"id":"e1","match":true}],"name":"frank"}`)
	if err := vjson.Unmarshal(data, &h); err != nil {
		t.Fatalf("Decode: %v", err)
	}
	if h.Name != "frank" {
		t.Errorf("Name = %q, want frank", h.Name)
	}
}

// TestRootStreamEmpty verifies that a root Stream[T] receiving an empty
// array still activates the handler with an empty batch. This covers the
// document_start empty-[] stream yield path (BIND_PHASE_ARRAY_CLOSE on
// empty open), which the prior flag-based implementation missed entirely.
func TestRootStreamEmpty(t *testing.T) {
	activated := false
	var rs stream.Stream[event]
	rs.OnRead(func(s stream.Scope[event]) error {
		activated = true
		for range s.Iter() {
			t.Error("unexpected item in empty root stream")
		}
		return nil
	})
	if err := vjson.Unmarshal([]byte(`[]`), &rs); err != nil {
		t.Fatalf("Decode root empty: %v", err)
	}
	if !activated {
		t.Error("root empty stream handler not activated")
	}
}

// TestRootStreamNonEmpty verifies a non-empty root Stream[T] activates the
// handler and yields all elements. This exercises document_start's non-empty
// '[' branch with BIND_KIND_STREAM at the root (bind_push_array_or_slice +
// array_begin + array_value + array_continue ']' close yield).
func TestRootStreamNonEmpty(t *testing.T) {
	var rs stream.Stream[event]
	var ids []string
	rs.OnRead(func(s stream.Scope[event]) error {
		for it := range s.Iter() {
			if err := it.Decode(); err != nil {
				return err
			}
			ids = append(ids, it.Target().ID)
		}
		return nil
	})
	if err := vjson.Unmarshal([]byte(`[{"id":"a"},{"id":"b"},{"id":"c"}]`), &rs); err != nil {
		t.Fatalf("Decode root: %v", err)
	}
	want := []string{"a", "b", "c"}
	if len(ids) != len(want) {
		t.Fatalf("ids = %v, want %v", ids, want)
	}
	for i := range want {
		if ids[i] != want[i] {
			t.Errorf("ids[%d] = %q, want %q", i, ids[i], want[i])
		}
	}
}

// TestMapDecodeStreamRejected verifies that vbind rejects a Stream[T] as a
// map value at build time. Map values are dynamically dispatched (the slot
// is recycled across entries), so there is no stable Stream[T] field
// instance on which to pre-register a handler. The map_value path also
// memsets the value slot, which would wipe any handler pointer.
func TestMapDecodeStreamRejected(t *testing.T) {
	type bad struct {
		M map[string]stream.Stream[event] `json:"m"`
	}
	var b bad
	err := vjson.Unmarshal([]byte(`{"m":{}}`), &b)
	if err == nil {
		t.Fatal("expected error for stream as map value, got nil")
	}
}

// TestStreamElementAllowed verifies that Stream[T] element types containing
// nested Stream fields are now accepted (previously build-rejected). The
// per-element yield path (BIND_FLAG_ELEM_HAS_STREAM) lets Go register nested
// OnRead via Item.Target() before the element body binds.
func TestStreamElementAllowed(t *testing.T) {
	t.Run("indirect", func(t *testing.T) {
		type elem struct {
			Inner stream.Stream[event] `json:"inner"`
		}
		var s stream.Stream[elem]
		// Build should succeed; empty array yields no handler activation.
		if err := vjson.Unmarshal([]byte(`[]`), &s); err != nil {
			t.Fatalf("expected success for []struct{Stream[T]}, got %v", err)
		}
	})
}

// TestStreamPointerElementRejected verifies that Stream[T] rejects a pointer
// element type. The slice backing holds T by value and native writes element
// slots in place; a pointer T would need per-element heap allocation and break
// the value-slot model.
func TestStreamPointerElementRejected(t *testing.T) {
	var s stream.Stream[*event]
	err := vjson.Unmarshal([]byte(`[]`), &s)
	if err == nil {
		t.Fatal("expected error for Stream[*T], got nil")
	}
}

// TestStreamRecursiveElementRejected verifies that Stream[T] rejects an element
// type that transitively contains Stream[T] of itself. Such a cycle through a
// Stream edge makes the stream type tree non-terminating and the per-element
// yield model recursive. Regular recursion (a struct containing []itself) is
// unaffected; only cycles crossing a Stream edge are rejected.
func TestStreamRecursiveElementRejected(t *testing.T) {
	type node struct {
		Children stream.Stream[node] `json:"children"`
	}
	var s stream.Stream[node]
	err := vjson.Unmarshal([]byte(`[]`), &s)
	if err == nil {
		t.Fatal("expected error for recursive Stream[T], got nil")
	}
}

// TestStreamNestedIter exercises the per-element yield path (non-leaf stream):
// Stream[User] where User contains a nested Stream[Event]. The handler must
// Target() and register Events.OnRead before Decode() binds the User body.
func TestStreamNestedIter(t *testing.T) {
	type user struct {
		Events stream.Stream[event] `json:"events"`
		ID     string               `json:"id"`
	}
	type response struct {
		Users   stream.Stream[user] `json:"users"`
		Message string              `json:"message"`
	}

	var r response
	var userIDs, eventIDs []string

	r.Users.OnRead(func(users stream.Scope[user]) error {
		for userItem := range users.Iter() {
			target := userItem.Target()
			target.Events.OnRead(func(events stream.Scope[event]) error {
				for eventItem := range events.Iter() {
					if err := eventItem.Decode(); err != nil {
						return err
					}
					eventIDs = append(eventIDs, eventItem.Target().ID)
				}
				return nil
			})
			if err := userItem.Decode(); err != nil {
				return err
			}
			userIDs = append(userIDs, target.ID)
		}
		return nil
	})

	data := []byte(`{"users":[{"id":"u1","events":[{"id":"e1","match":false},{"id":"e2","match":true}]},{"id":"u2","events":[{"id":"e3","match":false}]}],"message":"done"}`)
	if err := vjson.Unmarshal(data, &r); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if r.Message != "done" {
		t.Errorf("Message = %q, want done", r.Message)
	}
	wantUsers := []string{"u1", "u2"}
	if len(userIDs) != len(wantUsers) {
		t.Fatalf("userIDs = %v, want %v", userIDs, wantUsers)
	}
	for i := range wantUsers {
		if userIDs[i] != wantUsers[i] {
			t.Errorf("userIDs[%d] = %q, want %q", i, userIDs[i], wantUsers[i])
		}
	}
	wantEvents := []string{"e1", "e2", "e3"}
	if len(eventIDs) != len(wantEvents) {
		t.Fatalf("eventIDs = %v, want %v", eventIDs, wantEvents)
	}
	for i := range wantEvents {
		if eventIDs[i] != wantEvents[i] {
			t.Errorf("eventIDs[%d] = %q, want %q", i, eventIDs[i], wantEvents[i])
		}
	}
}

// TestStreamInnerBreakOuter exercises the cross-scope Break propagation path:
// an inner stream handler returns outer.Break(), the signal routes via
// streamScopes to the outer scope, and the outer handler's IsBreak recognizes
// it and executes a native break. Verifies the README §"从内层跳出外层流" UX.
func TestStreamInnerBreakOuter(t *testing.T) {
	type event struct {
		ID    string `json:"id"`
		Match bool   `json:"match"`
	}
	type user struct {
		Events stream.Stream[event] `json:"events"`
		ID     string               `json:"id"`
	}
	type response struct {
		Users   stream.Stream[user] `json:"users"`
		Message string              `json:"message"`
	}

	var r response
	var found event
	var userIDsBeforeBreak []string

	r.Users.OnRead(func(users stream.Scope[user]) error {
		for userItem := range users.Iter() {
			target := userItem.Target()
			target.Events.OnRead(func(events stream.Scope[event]) error {
				for eventItem := range events.Iter() {
					if err := eventItem.Decode(); err != nil {
						return err
					}
					e := eventItem.Target()
					if e.Match {
						found = *e
						return users.Break()
					}
				}
				return nil
			})

			err := userItem.Decode()
			if users.IsBreak(err) {
				break
			}
			if err != nil {
				return err
			}
			userIDsBeforeBreak = append(userIDsBeforeBreak, target.ID)
		}
		return nil
	})

	// u1 has no matching event; u2's e2 matches → break out of Users.
	data := []byte(`{"users":[{"id":"u1","events":[{"id":"e1","match":false}]},{"id":"u2","events":[{"id":"e2","match":true},{"id":"e3","match":false}]},{"id":"u3","events":[]}],"message":"done"}`)
	if err := vjson.Unmarshal(data, &r); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if r.Message != "done" {
		t.Errorf("Message = %q, want done", r.Message)
	}
	if found.ID != "e2" || !found.Match {
		t.Errorf("found = %+v, want {ID:e2 Match:true}", found)
	}
	// u1 was fully processed before break; u2's consumeUser is skipped (break
	// before consumeUser). u3 never reached.
	wantUsers := []string{"u1"}
	if len(userIDsBeforeBreak) != len(wantUsers) {
		t.Fatalf("userIDsBeforeBreak = %v, want %v", userIDsBeforeBreak, wantUsers)
	}
}

// TestStreamThreeLayerBreak exercises the full three-layer call stack from
// README §"思考" L462-483: innermost Events handler returns outermost
// users.Break(), the signal routes along streamScopes through the middle
// Sessions scope, and the outermost Users scope recognizes it via IsBreak.
func TestStreamThreeLayerBreak(t *testing.T) {
	type leaf struct {
		ID    string `json:"id"`
		Match bool   `json:"match"`
	}
	type middle struct {
		Leaves stream.Stream[leaf] `json:"leaves"`
		ID     string              `json:"id"`
	}
	type outer struct {
		Middles stream.Stream[middle] `json:"middles"`
		Label   string                `json:"label"`
	}
	type root struct {
		Outers stream.Stream[outer] `json:"outers"`
		Tag    string               `json:"tag"`
	}

	var r root
	var found leaf
	var middlesSeen []string

	r.Outers.OnRead(func(outers stream.Scope[outer]) error {
		for outerItem := range outers.Iter() {
			outerTarget := outerItem.Target()
			outerTarget.Middles.OnRead(func(middles stream.Scope[middle]) error {
				for middleItem := range middles.Iter() {
					middleTarget := middleItem.Target()
					middleTarget.Leaves.OnRead(func(leaves stream.Scope[leaf]) error {
						for leafItem := range leaves.Iter() {
							if err := leafItem.Decode(); err != nil {
								return err
							}
							l := leafItem.Target()
							if l.Match {
								found = *l
								return outers.Break() // jump to outermost Outers scope
							}
						}
						return nil
					})

					err := middleItem.Decode()
					if outers.IsBreak(err) {
						// Signal targets outers, not middles: propagate.
						return err
					}
					if err != nil {
						return err
					}
					middlesSeen = append(middlesSeen, middleItem.Target().ID)
				}
				return nil
			})

			err := outerItem.Decode()
			if outers.IsBreak(err) {
				break
			}
			if err != nil {
				return err
			}
		}
		return nil
	})

	// o1.m1 has no match; o1.m2.l2 matches → break out of Outers.
	data := []byte(`{
		"outers":[
			{"label":"o1","middles":[
				{"id":"m1","leaves":[{"id":"l1","match":false}]},
				{"id":"m2","leaves":[{"id":"l2","match":true},{"id":"l3","match":false}]}
			]},
			{"label":"o2","middles":[{"id":"m3","leaves":[]}]}
		],
		"tag":"end"
	}`)
	if err := vjson.Unmarshal(data, &r); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if r.Tag != "end" {
		t.Errorf("Tag = %q, want end", r.Tag)
	}
	if found.ID != "l2" || !found.Match {
		t.Errorf("found = %+v, want {ID:l2 Match:true}", found)
	}
	// m1 fully processed; m2's middleItem.Decode returns the break signal
	// (outers-scoped), so m2 is not appended.
	wantMiddles := []string{"m1"}
	if len(middlesSeen) != len(wantMiddles) {
		t.Fatalf("middlesSeen = %v, want %v", middlesSeen, wantMiddles)
	}
}
