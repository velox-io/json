package benchmark

import _ "embed"

var SmallJSON = []byte(`{
	"bool": true,
	"int": 42,
	"int64": 9223372036854775807,
	"float64": 3.14159265358979,
	"string": "hello world benchmark"
}`)

var NestedJSON = []byte(`{
	"id": 12345,
	"name": "Alice Johnson",
	"email": "alice@example.com",
	"active": true,
	"score": 95.5,
	"address": {
		"city": "San Francisco",
		"country": "USA",
		"zip": 94102
	}
}`)

var SliceJSON = []byte(`{
	"users": [
		{"id": 1, "name": "Alice", "email": "alice@example.com", "active": true, "score": 95.5, "address": {"city": "NYC", "country": "USA", "zip": 10001}},
		{"id": 2, "name": "Bob", "email": "bob@example.com", "active": false, "score": 82.3, "address": {"city": "LA", "country": "USA", "zip": 90001}},
		{"id": 3, "name": "Charlie", "email": "charlie@example.com", "active": true, "score": 71.0, "address": {"city": "Chicago", "country": "USA", "zip": 60601}},
		{"id": 4, "name": "Diana", "email": "diana@example.com", "active": true, "score": 99.9, "address": {"city": "Seattle", "country": "USA", "zip": 98101}},
		{"id": 5, "name": "Eve", "email": "eve@example.com", "active": false, "score": 60.1, "address": {"city": "Boston", "country": "USA", "zip": 2101}}
	]
}`)

//go:embed testdata/escape_heavy.json
var EscapeHeavyJSON []byte

//go:embed testdata/pods.json
var PodsJSON []byte

//go:embed testdata/twitter.json
var TwitterJSON []byte
