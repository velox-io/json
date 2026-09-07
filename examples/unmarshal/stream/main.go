// Package main demonstrates the streaming JSON binding API: OnRead handlers
// consume decoded elements one at a time, and OnWrite producers encode them
// without either side materializing the whole array.
package main

import (
	"fmt"
	"os"
	"strings"

	vjson "github.com/velox-io/json"
	"github.com/velox-io/json/stream"
)

// User is the element type for the basic streaming examples.
type User struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

// Grant is the element type for the parallel-siblings example.
type Grant struct {
	UserID string `json:"user_id"`
	Action string `json:"action"`
}

// Response carries the streams exercised by the examples. Fields are
// semantic siblings: which handler runs first depends only on JSON member
// order, not on handler registration order.
type Response struct {
	Users   vjson.Stream[User]  `json:"users"`
	Grants  vjson.Stream[Grant] `json:"grants"`
	Message string              `json:"message"`
}

// Event is the element type for the nested-stream examples.
type Event struct {
	ID    string `json:"id"`
	Match bool   `json:"match"`
}

// UserWithEvents is a non-leaf stream element: each user carries its own
// nested Stream[Event]. The handler must register Events.OnRead via
// Item.Target before Item.Decode binds the user body, so the nested handler
// is active when the parser reaches the events array.
type UserWithEvents struct {
	Events vjson.Stream[Event] `json:"events"`
	ID     string              `json:"id"`
}

// NestedResponse carries a non-leaf stream for the nested-stream examples.
type NestedResponse struct {
	Users   vjson.Stream[UserWithEvents] `json:"users"`
	Message string                       `json:"message"`
}

// main runs every demo when invoked without arguments; with one argument it
// runs only the named demo (for example "writeCursor"). An unknown name lists
// the available demos.
func main() {
	demos := []struct {
		name string
		fn   func() error
	}{
		{"basicIter", basicIter},
		{"streamBreak", streamBreak},
		{"allowValueReuse", allowValueReuse},
		{"parallelSiblings", parallelSiblings},
		{"nestedStream", nestedStream},
		{"innerBreakOuter", innerBreakOuter},
		{"writeBasic", writeBasic},
		{"writeEncoder", writeEncoder},
		{"writeNested", writeNested},
		{"writeCursor", writeCursor},
	}

	if len(os.Args) > 1 {
		for _, d := range demos {
			if d.name == os.Args[1] {
				if err := d.fn(); err != nil {
					panic(err)
				}
				return
			}
		}
		fmt.Fprintf(os.Stderr, "unknown demo %q, available:", os.Args[1])
		for _, d := range demos {
			fmt.Fprintf(os.Stderr, " %s", d.name)
		}
		fmt.Fprintln(os.Stderr)
		os.Exit(1)
	}

	for _, d := range demos {
		if err := d.fn(); err != nil {
			panic(err)
		}
	}
}

// basicIter exercises the basic iteration pattern: register a handler
// before Decode, iterate Items, Decode each element, then access fields that
// follow the stream in the JSON object after Decode returns.
func basicIter() error {
	input := []byte(`{
		"users": [
			{"id": "u1", "name": "alice"},
			{"id": "u2", "name": "bob"}
		],
		"message": "ok"
	}`)

	var response Response
	var ids []string

	response.Users.OnRead(func(users stream.Scope[User]) error {
		for item := range users.Iter() {
			if err := item.Decode(); err != nil {
				return err
			}
			ids = append(ids, item.Target().ID)
		}
		return nil
	})

	if err := vjson.Unmarshal(input, &response); err != nil {
		return err
	}

	fmt.Println("ids:", strings.Join(ids, ","))
	fmt.Println("message:", response.Message)
	return nil
}

// streamBreak exercises current-layer break: native Go `break` ends the
// stream, the parser drains the remaining array elements, and the outer
// object resumes binding. No nested handler is involved.
func streamBreak() error {
	input := []byte(`{
		"users": [
			{"id": "u1", "name": "alice"},
			{"id": "u2", "name": "bob"},
			{"id": "u3", "name": "carol"}
		],
		"message": "done"
	}`)

	var response Response
	var found *User

	response.Users.OnRead(func(users stream.Scope[User]) error {
		for item := range users.Iter() {
			if err := item.Decode(); err != nil {
				return err
			}
			user := item.Target()
			if user.Name == "bob" {
				found = user
				break
			}
		}
		return nil
	})

	if err := vjson.Unmarshal(input, &response); err != nil {
		return err
	}

	if found == nil {
		return fmt.Errorf("user not found")
	}
	fmt.Println("found:", found.ID)
	fmt.Println("message:", response.Message)
	return nil
}

// allowValueReuse exercises the reuse license: the parser may overwrite
// the previous element's storage on the next iteration. The handler must
// consume or copy each value before requesting the next item.
func allowValueReuse() error {
	input := []byte(`{
		"users": [
			{"id": "u1", "name": "alice"},
			{"id": "u2", "name": "bob"},
			{"id": "u3", "name": "carol"}
		]
	}`)

	var response Response
	var names []string

	response.Users.OnRead(func(users stream.Scope[User]) error {
		users.AllowValueReuse()

		for item := range users.Iter() {
			if err := item.Decode(); err != nil {
				return err
			}
			// Copy out before the next iteration: under reuse the parser
			// may overwrite this storage when the next item is requested.
			names = append(names, item.Target().Name)
		}
		return nil
	})

	if err := vjson.Unmarshal(input, &response); err != nil {
		return err
	}

	fmt.Println("names:", strings.Join(names, ","))
	return nil
}

// parallelSiblings exercises parallel streams on the same object:
// Users and Grants are semantic siblings whose handler execution order
// follows JSON member order. When the stream appearing first depends on
// data from the stream appearing later, the caller buffers the out-of-order
// items and reconciles when the dependency arrives.
func parallelSiblings() error {
	// Grants appear before Users in the input, but each grant references a
	// user by ID. The grants handler buffers grants by user ID; when the
	// users handler runs later, it joins each user with its pending grants.
	input := []byte(`{
		"grants": [
			{"user_id": "u1", "action": "read"},
			{"user_id": "u2", "action": "write"},
			{"user_id": "u1", "action": "delete"}
		],
		"users": [
			{"id": "u1", "name": "alice"},
			{"id": "u2", "name": "bob"}
		],
		"message": "ok"
	}`)

	var response Response
	pendingGrants := make(map[string][]Grant)
	var joined []string

	response.Grants.OnRead(func(grants stream.Scope[Grant]) error {
		for item := range grants.Iter() {
			if err := item.Decode(); err != nil {
				return err
			}
			grant := item.Target()
			pendingGrants[grant.UserID] = append(pendingGrants[grant.UserID], *grant)
		}
		return nil
	})

	response.Users.OnRead(func(users stream.Scope[User]) error {
		for item := range users.Iter() {
			if err := item.Decode(); err != nil {
				return err
			}
			user := item.Target()
			for _, g := range pendingGrants[user.ID] {
				joined = append(joined, user.Name+":"+g.Action)
			}
		}
		return nil
	})

	if err := vjson.Unmarshal(input, &response); err != nil {
		return err
	}

	fmt.Println("joined:", strings.Join(joined, ","))
	fmt.Println("message:", response.Message)
	return nil
}

// nestedStream exercises a non-leaf stream: each element carries its own
// nested Stream field. The handler registers the nested OnRead via
// Item.Target before Item.Decode drives the body bind, so the nested handler
// is active by the time the parser reaches the events array.
//
// Execution order per element: Target returns the unbound destination,
// OnRead registers the nested handler, Decode binds the user body (the
// nested handler fires inside this call when the parser reaches events),
// then the outer loop reads the now-complete user via Target.
func nestedStream() error {
	input := []byte(`{
		"users": [
			{"id": "u1", "events": [{"id": "e1", "match": false}, {"id": "e2", "match": true}]},
			{"id": "u2", "events": [{"id": "e3", "match": false}]}
		],
		"message": "done"
	}`)

	var response NestedResponse
	var userIDs, eventIDs []string

	response.Users.OnRead(func(users stream.Scope[UserWithEvents]) error {
		for userItem := range users.Iter() {
			target := userItem.Target()
			target.Events.OnRead(func(events stream.Scope[Event]) error {
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

	if err := vjson.Unmarshal(input, &response); err != nil {
		return err
	}

	fmt.Println("users:", strings.Join(userIDs, ","))
	fmt.Println("events:", strings.Join(eventIDs, ","))
	fmt.Println("message:", response.Message)
	return nil
}

// innerBreakOuter exercises cross-scope break: an inner stream handler
// returns outer.Break() to jump out of the outer iteration. A native label
// break cannot cross nested handler invocations, so Scope.Break/IsBreak is
// the only way to terminate an outer stream from inside an inner one.
//
// The inner handler returns the signal unmodified; the outer Item.Decode
// returns it as an error; the outer loop recognizes it via IsBreak and
// executes a native break. After the break the parser drains the rest of
// the Users array and resumes binding the enclosing object.
func innerBreakOuter() error {
	input := []byte(`{
		"users": [
			{"id": "u1", "events": [{"id": "e1", "match": false}]},
			{"id": "u2", "events": [{"id": "e2", "match": true}, {"id": "e3", "match": false}]},
			{"id": "u3", "events": []}
		],
		"message": "done"
	}`)

	var response NestedResponse
	var found Event
	var seenBeforeBreak []string

	response.Users.OnRead(func(users stream.Scope[UserWithEvents]) error {
		for userItem := range users.Iter() {
			target := userItem.Target()
			target.Events.OnRead(func(events stream.Scope[Event]) error {
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
			seenBeforeBreak = append(seenBeforeBreak, target.ID)
		}
		return nil
	})

	if err := vjson.Unmarshal(input, &response); err != nil {
		return err
	}

	fmt.Println("found:", found.ID)
	fmt.Println("seen before break:", strings.Join(seenBeforeBreak, ","))
	fmt.Println("message:", response.Message)
	return nil
}

// writeBasic exercises the write-side counterpart of basicIter: OnWrite
// registers a producer that pushes elements into a Sink one at a time while
// the encoder owns the array framing. The producer may reuse one element
// slot across Encode calls: the encoder has read the element fully by the
// time Encode returns.
func writeBasic() error {
	// A Stream field with no registered producer is a configuration error:
	// encoding has no data source for the field.
	if _, err := vjson.Marshal(&Response{}); err != nil {
		fmt.Println("unconfigured:", err)
	}

	var resp Response
	resp.Users.OnWrite(func(sink stream.Sink[User]) error {
		var u User
		for i, name := range []string{"alice", "bob"} {
			u.ID = fmt.Sprintf("u%d", i+1)
			u.Name = name
			if err := sink.Encode(&u); err != nil {
				return err
			}
		}
		return nil
	})
	resp.Grants.OnWrite(func(sink stream.Sink[Grant]) error {
		g := Grant{UserID: "u1", Action: "read"}
		if err := sink.Encode(&g); err != nil {
			return err
		}
		g.Action = "write"
		return sink.Encode(&g)
	})
	resp.Message = "ok"

	out, err := vjson.MarshalIndent(&resp, "", "\t")
	if err != nil {
		return err
	}
	fmt.Println(string(out))
	return nil
}

// countingWriter counts bytes and write calls without retaining output.
type countingWriter struct {
	bytes  int
	writes int
}

func (w *countingWriter) Write(p []byte) (int, error) {
	w.bytes += len(p)
	w.writes++
	return len(p), nil
}

// writeEncoder drives a root-level stream through an Encoder: elements are
// generated lazily and flushed to the writer at a low-water mark, so both
// the producer state and the encoder window stay bounded regardless of the
// array length.
func writeEncoder() error {
	const elems = 1_000_000
	w := &countingWriter{}
	enc := vjson.NewEncoder(w)

	var ints vjson.Stream[int]
	ints.OnWrite(func(sink stream.Sink[int]) error {
		for i := range elems {
			if err := sink.Encode(&i); err != nil {
				return err
			}
		}
		return nil
	})

	if err := enc.Encode(&ints); err != nil {
		return err
	}
	fmt.Printf("root stream: %d elements, %d bytes, %d writes\n", elems, w.bytes, w.writes)
	return nil
}

// writeNested exercises the write-side counterpart of nestedStream: each
// element carries its own nested Stream, and the producer registers the
// nested OnWrite on the element before submitting it, mirroring the read
// side's ordering of Target before Decode.
func writeNested() error {
	users := []struct {
		id     string
		events []Event
	}{
		{"u1", []Event{{ID: "e1"}, {ID: "e2", Match: true}}},
		{"u2", []Event{{ID: "e3"}}},
	}

	var resp NestedResponse
	resp.Users.OnWrite(func(sink stream.Sink[UserWithEvents]) error {
		for _, u := range users {
			elem := UserWithEvents{ID: u.id}
			events := u.events
			elem.Events.OnWrite(func(sink stream.Sink[Event]) error {
				for i := range events {
					if err := sink.Encode(&events[i]); err != nil {
						return err
					}
				}
				return nil
			})
			if err := sink.Encode(&elem); err != nil {
				return err
			}
		}
		return nil
	})
	resp.Message = "done"

	out, err := vjson.Marshal(&resp)
	if err != nil {
		return err
	}
	fmt.Println(string(out))
	return nil
}

// Item is the element type of the database-export demo: one row per sink
// call, produced by the mock cursor on demand.
type Item struct {
	ID   int    `json:"id"`
	Name string `json:"name"`
}

// PageData nests the stream one struct level below the response root, the
// common API envelope shape: response.Data.Items. Total is declared before
// Items so the summary precedes the array in the output.
type PageData struct {
	Total int                `json:"total"`
	Items vjson.Stream[Item] `json:"items"`
}

// Envelope is the outer response object of the database-export demo.
type Envelope struct {
	Data  PageData `json:"data"`
	Error string   `json:"error"`
}

// cursor mocks a database result set with the surface of database/sql's
// Rows: Next advances, Scan fills the destination, Err reports traversal
// failures. Rows are generated on demand, so the "table" is never held in
// memory. Swap it for a real *sql.Rows and the demo body is unchanged.
type cursor struct {
	rows int
	i    int
}

func (c *cursor) Next() bool {
	if c.i >= c.rows {
		return false
	}
	c.i++
	return true
}

func (c *cursor) Scan(item *Item) error {
	item.ID = c.i
	item.Name = fmt.Sprintf("item-%d", c.i)
	return nil
}

func (c *cursor) Err() error { return nil }

// headTailWriter keeps the first and last bytes of the output and counts
// writes, so a huge document can be shown as its shape plus totals.
type headTailWriter struct {
	head, tail int
	headBuf    []byte
	tailBuf    []byte
	bytes      int
	writes     int
}

func (w *headTailWriter) Write(p []byte) (int, error) {
	w.bytes += len(p)
	w.writes++
	if n := w.head - len(w.headBuf); n > 0 {
		w.headBuf = append(w.headBuf, p[:min(len(p), n)]...)
	}
	w.tailBuf = append(w.tailBuf, p...)
	if len(w.tailBuf) > w.tail {
		w.tailBuf = w.tailBuf[len(w.tailBuf)-w.tail:]
	}
	return len(p), nil
}

// writeCursor exercises the database-export shape: a cursor feeds a nested
// response.Data.Items stream through an Encoder. The cursor yields rows one
// at a time and the encoder flushes at a low-water mark, so neither the row
// source nor the encoded array is materialized. Peak memory is one row plus
// the output window, regardless of the row count.
func writeCursor() error {
	cur := &cursor{rows: 100_000}

	var resp Envelope
	resp.Error = "ok"
	resp.Data.Total = cur.rows
	resp.Data.Items.OnWrite(func(sink stream.Sink[Item]) error {
		var item Item
		for cur.Next() {
			if err := cur.Scan(&item); err != nil {
				return err
			}
			if err := sink.Encode(&item); err != nil {
				return err
			}
		}
		return cur.Err()
	})

	w := &headTailWriter{head: 96, tail: 80}
	if err := vjson.NewEncoder(w).Encode(&resp); err != nil {
		return err
	}
	fmt.Printf("cursor export: %d rows, %d bytes, %d writes\n%s ... %s\n",
		cur.rows, w.bytes, w.writes, w.headBuf, w.tailBuf)
	return nil
}
