package benchmark

import (
	"encoding/json"
	"reflect"
	"testing"

	"dev.local/benchmark/twitter"

	vjson "github.com/velox-io/json"
)

// TestNativeEncoder_Twitter validates that velox's native encoder produces
// semantically identical output to encoding/json for the twitter.json payload.
//
// This is a real-world integration test: TwitterStruct contains deeply
// nested structs, []interface{} fields, interface{} fields, and strings
// with non-ASCII (Japanese) text — exercising hot resume, pointer
// encoding, and string escaping in the native C engine.
//
// We compare semantically (via re-parse + DeepEqual) because:
//   - encoding/json iterates map keys in sorted order; velox may differ
//     for interface{}-typed maps that go through Go fallback
//   - encoding/json escapes <, >, & by default; velox requires WithStdCompat
func TestNativeEncoder_Twitter(t *testing.T) {
	// 1. Unmarshal twitter.json into typed struct using stdlib.
	data := LoadTwitterCompactJSON()
	var tw twitter.TwitterStruct
	if err := json.Unmarshal(data, &tw); err != nil {
		t.Fatalf("stdlib Unmarshal: %v", err)
	}

	// 2. Marshal with encoding/json (reference).
	stdOut, err := json.Marshal(&tw)
	if err != nil {
		t.Fatalf("stdlib Marshal: %v", err)
	}

	// 3. Marshal with velox (StdCompat for HTML escaping parity).
	vjOut, err := vjson.Marshal(&tw, vjson.WithStdCompat())
	if err != nil {
		t.Fatalf("velox Marshal: %v", err)
	}

	// 4. Compare semantically: re-parse both and DeepEqual.
	assertJSONEqual(t, "TwitterStruct", stdOut, vjOut)
}

// TestNativeEncoder_Twitter_ByteExact tests byte-for-byte equality
// on the pure-native SearchMetadata struct (no interface{} fields,
// no map key ordering issues).
func TestNativeEncoder_Twitter_ByteExact(t *testing.T) {
	data := LoadTwitterCompactJSON()
	var tw twitter.TwitterStruct
	if err := json.Unmarshal(data, &tw); err != nil {
		t.Fatalf("stdlib Unmarshal: %v", err)
	}

	stdOut, err := json.Marshal(&tw.SearchMetadata)
	if err != nil {
		t.Fatalf("stdlib Marshal: %v", err)
	}
	vjOut, err := vjson.Marshal(&tw.SearchMetadata, vjson.WithStdCompat())
	if err != nil {
		t.Fatalf("velox Marshal: %v", err)
	}

	if string(vjOut) != string(stdOut) {
		diffIdx := firstDiff(stdOut, vjOut)
		t.Errorf("SearchMetadata byte mismatch at %d (std len=%d, velox len=%d)\n"+
			"  std:   %s\n"+
			"  velox: %s",
			diffIdx, len(stdOut), len(vjOut), excerpt(stdOut, diffIdx), excerpt(vjOut, diffIdx))
	}
}

// TestNativeEncoder_Twitter_Statuses verifies per-status consistency.
// Each status is marshalled individually so that any mismatch can be
// pinpointed to a specific array element.
func TestNativeEncoder_Twitter_Statuses(t *testing.T) {
	data := LoadTwitterCompactJSON()
	var tw twitter.TwitterStruct
	if err := json.Unmarshal(data, &tw); err != nil {
		t.Fatalf("stdlib Unmarshal: %v", err)
	}

	for i, status := range tw.Statuses {
		stdOut, err := json.Marshal(&status)
		if err != nil {
			t.Fatalf("status[%d] stdlib Marshal: %v", i, err)
		}
		vjOut, err := vjson.Marshal(&status, vjson.WithStdCompat())
		if err != nil {
			t.Fatalf("status[%d] velox Marshal: %v", i, err)
		}
		assertJSONEqual(t, "status", stdOut, vjOut)
	}
}

// TestNativeEncoder_Twitter_Users verifies per-user consistency.
// User structs have many string fields and interface{} fields,
// testing the hot resume path thoroughly.
func TestNativeEncoder_Twitter_Users(t *testing.T) {
	data := LoadTwitterCompactJSON()
	var tw twitter.TwitterStruct
	if err := json.Unmarshal(data, &tw); err != nil {
		t.Fatalf("stdlib Unmarshal: %v", err)
	}

	for i, status := range tw.Statuses {
		stdOut, err := json.Marshal(&status.User)
		if err != nil {
			t.Fatalf("user[%d] stdlib Marshal: %v", i, err)
		}
		vjOut, err := vjson.Marshal(&status.User, vjson.WithStdCompat())
		if err != nil {
			t.Fatalf("user[%d] velox Marshal: %v", i, err)
		}
		assertJSONEqual(t, status.User.ScreenName, stdOut, vjOut)
	}
}

// ---- helpers ----

// assertJSONEqual re-parses both JSON outputs into interface{} and
// compares with reflect.DeepEqual. This handles map key ordering
// differences that arise from interface{}-typed fields.
func assertJSONEqual(t *testing.T, label string, stdOut, vjOut []byte) {
	t.Helper()

	var stdVal, vjVal interface{}
	if err := json.Unmarshal(stdOut, &stdVal); err != nil {
		t.Fatalf("%s: re-parse stdlib output: %v", label, err)
	}
	if err := json.Unmarshal(vjOut, &vjVal); err != nil {
		t.Fatalf("%s: re-parse velox output: %v\nvelox output: %.500s", label, err, vjOut)
	}
	if !reflect.DeepEqual(stdVal, vjVal) {
		t.Errorf("%s: semantic mismatch (std len=%d, velox len=%d)\n"+
			"  std:   %.200s\n"+
			"  velox: %.200s",
			label, len(stdOut), len(vjOut), stdOut, vjOut)
	}
}

// firstDiff returns the index of the first byte that differs between a and b.
func firstDiff(a, b []byte) int {
	n := len(a)
	if len(b) < n {
		n = len(b)
	}
	for i := 0; i < n; i++ {
		if a[i] != b[i] {
			return i
		}
	}
	return n
}

// excerpt returns a short substring around the given index for error messages.
func excerpt(data []byte, idx int) string {
	start := idx - 40
	if start < 0 {
		start = 0
	}
	end := idx + 40
	if end > len(data) {
		end = len(data)
	}
	return string(data[start:end])
}
