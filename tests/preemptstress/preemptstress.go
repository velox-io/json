// Package preemptstress drives the encoder VM through the runtime
// interleavings that decide its stack safety: async preemption signals
// landing inside an active C frame, concurrent mark scanning a stack
// whose top frames are native, and stack growth relocating a goroutine
// whose VM state points into the old stack.
//
// The VM executes on the goroutine stack behind a nosplit trampoline,
// so a signal or a scan arriving mid-execution must find a consistent
// frame chain. Each test runs many marshal workers under GC pressure,
// deep recursion, or aggressive collection to force those arrivals; a
// violation surfaces as a runtime diagnostic (SIGSEGV, "unexpected
// return pc"), which the gc-stress runner classifies as a crash.
package preemptstress

import (
	"fmt"
	"time"
)

// Status implements json.Marshaler from a mid-struct field position.
type Status struct {
	Code    int
	Message string
}

// MarshalJSON renders the status as a compact object.
func (s Status) MarshalJSON() ([]byte, error) {
	return []byte(`{"code":` + fmt.Sprint(s.Code) + `,"msg":"` + s.Message + `"}`), nil
}

// Base is the anonymous embed inside User, carrying a map[string]any.
type Base struct {
	ID        int            `json:"id"`
	CreatedBy string         `json:"created_by"`
	Attrs     map[string]any `json:"attrs"`
}

// Address is a plain nested struct; Detail goes through an any field,
// so nested values compile their Blueprints on the fly.
type Address struct {
	City   string `json:"city"`
	Street string `json:"street"`
	Detail any    `json:"detail,omitempty"`
}

// GeoLocation appears in the workload only inside interface values, so
// its Blueprint compiles at first sight during encoding.
type GeoLocation struct {
	Lat       float64  `json:"lat"`
	Lng       float64  `json:"lng"`
	Altitude  float64  `json:"altitude"`
	Accuracy  float64  `json:"accuracy"`
	Provider  string   `json:"provider"`
	Country   string   `json:"country"`
	Province  string   `json:"province"`
	District  string   `json:"district"`
	ZipCode   string   `json:"zip_code"`
	Timezone  string   `json:"timezone"`
	Timestamp int64    `json:"timestamp"`
	Tags      []string `json:"tags"`
}

// Tag is the element type of the User.Tags slice.
type Tag struct {
	Key   string `json:"key"`
	Value string `json:"value"`
}

// User covers the encoder paths in one struct: an anonymous embed, a
// Marshaler at a mid position, pointer and nil-pointer fields, a map of
// anonymous structs, and an any chain nesting GeoLocation.
type User struct {
	Base
	Name     string            `json:"name"`
	Age      int               `json:"age"`
	Addr     Address           `json:"addr"`
	WorkAt   *Address          `json:"work_at"`
	HomeAt   *Address          `json:"home_at"`
	Status   Status            `json:"status"`
	Tags     []Tag             `json:"tags"`
	Meta     map[string]string `json:"meta"`
	Contacts map[string]struct {
		Phone string `json:"phone"`
		Email string `json:"email"`
	} `json:"contacts"`
	Nickname string `json:"nickname"`
	Extra    any    `json:"extra"`
}

// NewTestUser returns a User with the field coverage described on the
// type, including a nil HomeAt and an Extra chain ending in GeoLocation.
func NewTestUser() User {
	return User{
		Base: Base{
			ID:        1,
			CreatedBy: "system",
			Attrs: map[string]any{
				"env":        "dev",
				"created_at": time.Date(2026, time.March, 6, 10, 0, 0, 0, time.UTC),
				"retry":      3,
				"enabled":    true,
			},
		},
		Name:   "alice",
		Age:    30,
		Addr:   Address{City: "Beijing", Street: "Chang'an Ave"},
		WorkAt: &Address{City: "Shanghai", Street: "Nanjing Rd"},
		Status: Status{Code: 200, Message: "active"},
		Tags: []Tag{
			{Key: "role", Value: "admin"},
			{Key: "dept", Value: "eng"},
			{Key: "level", Value: "senior"},
		},
		Meta: map[string]string{
			"source": "api",
			"region": "cn-east",
			"aa":     "cn-east",
			"bb":     "cn-east",
			"cc":     "cn-east",
		},
		Nickname: "lambit",
		Contacts: map[string]struct {
			Phone string `json:"phone"`
			Email string `json:"email"`
		}{
			"alice": {Phone: "13800001111", Email: "alice@example.com"},
			"bob":   {Phone: "13900002222", Email: "bob@example.com"},
		},
		Extra: Address{
			City:   "Shenzhen",
			Street: "Keyuan Rd",
			Detail: GeoLocation{
				Lat: 22.5431, Lng: 114.0579, Altitude: 15.3, Accuracy: 10.0,
				Provider: "gps", Country: "China", Province: "Guangdong",
				District: "Nanshan", ZipCode: "518057", Timezone: "Asia/Shanghai",
				Timestamp: 1709472000,
				Tags:      []string{"office", "primary", "verified"},
			},
		},
	}
}

// LargePayload keeps the VM busy for a long time per call: a struct
// array the VM walks without yielding, a large string-keyed map, and a
// map[string]any that forces interface cache lookups.
type LargePayload struct {
	Users    [64]User       `json:"users"`
	Index    map[string]int `json:"index"`
	Metadata map[string]any `json:"metadata"`
}

// BuildLargePayload returns the stress workload: 64 distinct users, a
// 200-entry index map, and nested metadata values.
func BuildLargePayload() LargePayload {
	var p LargePayload

	base := NewTestUser()
	for i := range p.Users {
		u := base
		u.Name = fmt.Sprintf("user_%d", i)
		u.Age = 20 + i
		u.Nickname = fmt.Sprintf("nick_%d", i)
		p.Users[i] = u
	}

	p.Index = make(map[string]int, 200)
	for i := 0; i < 200; i++ {
		p.Index[fmt.Sprintf("key_%04d", i)] = i
	}

	p.Metadata = map[string]any{
		"string_val": "hello world",
		"int_val":    42,
		"float_val":  3.14159,
		"bool_val":   true,
		"null_val":   nil,
		"nested_map": map[string]any{
			"a": "value_a",
			"b": 123,
			"c": map[string]any{
				"deep": true,
			},
		},
		"slice_val": []any{"x", "y", "z", 1, 2, 3},
	}

	return p
}
