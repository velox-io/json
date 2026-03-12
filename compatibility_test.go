package vjson

import (
	"bytes"
	"encoding/json"
	"flag"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"testing"
)

// go test -run TestJSONTestSuiteCompatibility . -update-compat-results

const compatResultsPath = "testdata/JSONTestSuite/compat_results.json"

var updateCompatResults = flag.Bool("update-compat-results", false, "update compatibility baseline for JSONTestSuite")

// compatResults stores compatibility differences between vjson and encoding/json.
//
// Naming follows jsonbench TestParseSuite style:
// - GotPassingWantFailing: vjson accepts, stdlib rejects
// - GotFailingWantPassing: vjson rejects, stdlib accepts
// - GotDifferentValues: both accept but decoded values differ after canonical JSON encoding
//
// Each entry is a JSONTestSuite filename (e.g. y_object_simple.json).
type compatResults struct {
	GotPassingWantFailing []string `json:"gotPassingWantFailing,omitempty"`
	GotFailingWantPassing []string `json:"gotFailingWantPassing,omitempty"`
	GotDifferentValues    []string `json:"gotDifferentValues,omitempty"`
}

func TestJSONTestSuiteCompatibility(t *testing.T) {
	names := jsonTestSuiteCaseNames(t)
	got := compatResults{}

	for _, name := range names {
		data := readJSONTestSuiteFile(t, name)

		stdlibValue, stdlibErr := parseWithStdlib(data)
		vjsonValue, vjsonErr := parseWithVJSON(data)

		switch {
		case stdlibErr != nil && vjsonErr == nil:
			got.GotPassingWantFailing = append(got.GotPassingWantFailing, name)
		case stdlibErr == nil && vjsonErr != nil:
			got.GotFailingWantPassing = append(got.GotFailingWantPassing, name)
		case stdlibErr == nil && vjsonErr == nil:
			if !equalCanonicalJSON(stdlibValue, vjsonValue) {
				got.GotDifferentValues = append(got.GotDifferentValues, name)
			}
		}
	}
	got.normalize()

	if *updateCompatResults {
		writeCompatResults(t, got)
		return
	}

	want := readCompatResults(t)
	want.normalize()

	if !reflect.DeepEqual(got, want) {
		t.Fatalf("compatibility mismatch (-got +want):\n%s", formatCompatDiff(t, got, want))
	}
}

func jsonTestSuiteCaseNames(t *testing.T) []string {
	t.Helper()

	names := make([]string, 0, 1024)
	names = append(names, jsonTestSuiteFileNames(t, "y_")...)
	names = append(names, jsonTestSuiteFileNames(t, "n_")...)
	names = append(names, jsonTestSuiteFileNames(t, "i_")...)
	sort.Strings(names)
	return names
}

func parseWithStdlib(data []byte) (any, error) {
	var v any
	err := json.Unmarshal(data, &v)
	return v, err
}

func parseWithVJSON(data []byte) (any, error) {
	var v any
	err := Unmarshal(data, &v)
	return v, err
}

func equalCanonicalJSON(x, y any) bool {
	bx, err := json.Marshal(x)
	if err != nil {
		return false
	}
	by, err := json.Marshal(y)
	if err != nil {
		return false
	}
	return bytes.Equal(bx, by)
}

func writeCompatResults(t *testing.T, results compatResults) {
	t.Helper()

	b, err := json.MarshalIndent(results, "", "  ")
	if err != nil {
		t.Fatalf("marshal compat results: %v", err)
	}
	b = append(b, '\n')

	if err := os.WriteFile(filepath.Clean(compatResultsPath), b, 0o664); err != nil {
		t.Fatalf("write compat results: %v", err)
	}
}

func readCompatResults(t *testing.T) compatResults {
	t.Helper()

	b, err := os.ReadFile(filepath.Clean(compatResultsPath))
	if err != nil {
		t.Fatalf("read compat results: %v (run with -update-compat-results)", err)
	}

	var out compatResults
	if err := json.Unmarshal(b, &out); err != nil {
		t.Fatalf("unmarshal compat results: %v", err)
	}
	return out
}

func (r *compatResults) normalize() {
	sort.Strings(r.GotPassingWantFailing)
	sort.Strings(r.GotFailingWantPassing)
	sort.Strings(r.GotDifferentValues)
}

func formatCompatDiff(t *testing.T, got, want compatResults) string {
	t.Helper()

	gotJSON, err := json.MarshalIndent(got, "", "  ")
	if err != nil {
		t.Fatalf("marshal got compat results: %v", err)
	}
	wantJSON, err := json.MarshalIndent(want, "", "  ")
	if err != nil {
		t.Fatalf("marshal want compat results: %v", err)
	}

	return "got:\n" + string(gotJSON) + "\nwant:\n" + string(wantJSON)
}
