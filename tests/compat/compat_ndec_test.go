//go:build !vdec && !vj_nondec

package vjson_test

import (
	"testing"

	vjson "github.com/velox-io/json"
)

// backendExpectedDivergences lists JSONTestSuite cases where the default
// native ndec backend intentionally diverges from encoding/json.
//
// The default ndec string path accepts raw control characters in string bodies.
// WithStrictScan rejects them during the root structural scan. The vdec backend
// validates control characters in its own scanner, so this list is empty there.
var backendGotPassingWantFailing = []string{
	"n_string_unescaped_ctrl_char.json",
	"n_string_unescaped_newline.json",
	"n_string_unescaped_tab.json",
}

// backendGotFailingWantPassing lists cases where the native ndec backend
// rejects input that encoding/json accepts. The native binder caps nesting at
// BIND_MAX_DEPTH (255) to bound the per-Parser frame array; encoding/json
// allows 10000.
var backendGotFailingWantPassing = []string{
	"i_structure_500_nested_arrays.json",
}

func TestStrictScanRejectsNativeControlCharacterDivergences(t *testing.T) {
	for _, name := range backendGotPassingWantFailing {
		t.Run(name, func(t *testing.T) {
			data := readJSONTestSuiteFile(t, name)
			var v any
			if err := vjson.Unmarshal(data, &v, vjson.WithStrictScan()); err == nil {
				t.Fatalf("WithStrictScan accepted %s", name)
			}
		})
	}
}
