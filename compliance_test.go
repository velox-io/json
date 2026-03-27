package vjson

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

const jsonTestSuiteDir = "tests/JSONTestSuite"

// knownComplianceAcceptDivergences lists y_*.json files where vjson incorrectly
// rejects valid JSON. These subtests are skipped with t.Skip() and a reason.
var knownComplianceAcceptDivergences = map[string]string{
	// No known accept divergences at this time.
}

// knownComplianceRejectDivergences lists n_*.json files where vjson intentionally
// accepts input that RFC 8259 says must be rejected.
var knownComplianceRejectDivergences = map[string]string{
	// vjson does not validate UTF-8 on the zero-copy string path
	// (deliberate performance trade-off). These files contain invalid
	// UTF-8 byte sequences inside strings that should be rejected per
	// RFC 8259, but vjson accepts them.
	//
	// Populate entries here when needed.
}

func TestJSONTestSuiteAccept(t *testing.T) {
	for _, name := range jsonTestSuiteFileNames(t, "y_") {
		t.Run(name, func(t *testing.T) {
			if reason, ok := knownComplianceAcceptDivergences[name]; ok {
				t.Skip("known divergence: " + reason)
			}

			data := readJSONTestSuiteFile(t, name)
			var v any
			if err := Unmarshal(data, &v); err != nil {
				t.Errorf("must accept but got error: %v", err)
			}
		})
	}
}

func TestJSONTestSuiteReject(t *testing.T) {
	for _, name := range jsonTestSuiteFileNames(t, "n_") {
		t.Run(name, func(t *testing.T) {
			if reason, ok := knownComplianceRejectDivergences[name]; ok {
				t.Skip("known divergence: " + reason)
			}

			data := readJSONTestSuiteFile(t, name)
			var v any
			if err := Unmarshal(data, &v); err == nil {
				t.Errorf("must reject but was accepted (value: %v)", v)
			}
		})
	}
}

func TestJSONTestSuiteImplementationDefined(t *testing.T) {
	for _, name := range jsonTestSuiteFileNames(t, "i_") {
		t.Run(name, func(t *testing.T) {
			data := readJSONTestSuiteFile(t, name)
			var v any
			if err := Unmarshal(data, &v); err != nil {
				t.Logf("REJECTED: %v", err)
			} else {
				t.Logf("ACCEPTED: %v", v)
			}
		})
	}
}

func jsonTestSuiteFileNames(t *testing.T, prefix string) []string {
	t.Helper()

	entries, err := os.ReadDir(jsonTestSuiteDir)
	if err != nil {
		t.Skipf("JSONTestSuite data not found: %v (run fetch script)", err)
	}

	names := make([]string, 0, len(entries))
	for _, e := range entries {
		name := e.Name()
		if strings.HasPrefix(name, prefix) && strings.HasSuffix(name, ".json") {
			names = append(names, name)
		}
	}
	sort.Strings(names)
	return names
}

func readJSONTestSuiteFile(t *testing.T, name string) []byte {
	t.Helper()

	data, err := os.ReadFile(filepath.Join(jsonTestSuiteDir, name))
	if err != nil {
		t.Fatal(err)
	}
	return data
}
