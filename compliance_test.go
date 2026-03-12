package vjson

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const jsonTestSuiteDir = "testdata/JSONTestSuite"

// knownAcceptDivergences lists y_*.json files where vjson incorrectly
// rejects valid JSON. These subtests are skipped with t.Skip() and a reason.
var knownAcceptDivergences = map[string]string{
	// No known accept divergences at this time.
}

// knownRejectDivergences lists n_*.json files where vjson intentionally
// accepts input that RFC 8259 says must be rejected.
var knownRejectDivergences = map[string]string{
	// vjson does not validate UTF-8 on the zero-copy string path
	// (deliberate performance trade-off). These files contain invalid
	// UTF-8 byte sequences inside strings that should be rejected per
	// RFC 8259, but vjson accepts them.
	//
	// Populated after initial test run identifies specific failures.
}

func TestJSONTestSuiteAccept(t *testing.T) {
	entries, err := os.ReadDir(jsonTestSuiteDir)
	if err != nil {
		t.Skipf("JSONTestSuite data not found: %v (run fetch script)", err)
	}

	for _, e := range entries {
		name := e.Name()
		if !strings.HasPrefix(name, "y_") || !strings.HasSuffix(name, ".json") {
			continue
		}
		t.Run(name, func(t *testing.T) {
			if reason, ok := knownAcceptDivergences[name]; ok {
				t.Skip("known divergence: " + reason)
			}
			data, err := os.ReadFile(filepath.Join(jsonTestSuiteDir, name))
			if err != nil {
				t.Fatal(err)
			}

			// Parse with stdlib first to get expected result
			var expected any
			if err := json.Unmarshal(data, &expected); err != nil {
				t.Fatalf("stdlib failed to parse: %v", err)
			}

			// Parse with vjson
			var got any
			if err := Unmarshal(data, &got); err != nil {
				t.Errorf("must accept but got error: %v", err)
				return
			}

			// Compare results
			expectedJSON, _ := json.Marshal(expected)
			gotJSON, _ := json.Marshal(got)
			if string(expectedJSON) != string(gotJSON) {
				t.Errorf("result mismatch:\nexpected: %s\ngot:      %s", expectedJSON, gotJSON)
			}
		})
	}
}

func TestJSONTestSuiteReject(t *testing.T) {
	entries, err := os.ReadDir(jsonTestSuiteDir)
	if err != nil {
		t.Skipf("JSONTestSuite data not found: %v (run fetch script)", err)
	}

	for _, e := range entries {
		name := e.Name()
		if !strings.HasPrefix(name, "n_") || !strings.HasSuffix(name, ".json") {
			continue
		}
		t.Run(name, func(t *testing.T) {
			if reason, ok := knownRejectDivergences[name]; ok {
				t.Skip("known divergence: " + reason)
			}
			data, err := os.ReadFile(filepath.Join(jsonTestSuiteDir, name))
			if err != nil {
				t.Fatal(err)
			}
			var v any
			if err := Unmarshal(data, &v); err == nil {
				t.Errorf("must reject but was accepted (value: %v)", v)
			}
		})
	}
}

func TestJSONTestSuiteImplementationDefined(t *testing.T) {
	entries, err := os.ReadDir(jsonTestSuiteDir)
	if err != nil {
		t.Skipf("JSONTestSuite data not found: %v (run fetch script)", err)
	}

	for _, e := range entries {
		name := e.Name()
		if !strings.HasPrefix(name, "i_") || !strings.HasSuffix(name, ".json") {
			continue
		}
		t.Run(name, func(t *testing.T) {
			data, err := os.ReadFile(filepath.Join(jsonTestSuiteDir, name))
			if err != nil {
				t.Fatal(err)
			}
			var v any
			if err := Unmarshal(data, &v); err != nil {
				t.Logf("REJECTED: %v", err)
			} else {
				t.Logf("ACCEPTED: %v", v)
			}
		})
	}
}
