// Package main demonstrates the streaming JSON binding API.
// See README.md for the full design.
package main

import (
	"fmt"
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

func main() {
	for _, demo := range []func() error{
		basicIter,
		streamBreak,
		allowValueReuse,
		parallelSiblings,
		nestedStream,
		innerBreakOuter,
	} {
		if err := demo(); err != nil {
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
