package venc

import (
	"encoding/json"
	"testing"

	"github.com/velox-io/json/native/encvm"
)

// Regression test: key pool overflow + MarshalIndent (full VM).
//
// When the 64KB global key pool is full, struct fields compile to opFallback
// and their values are encoded by the Go fallback path. Under indent the
// interp's opFallback used to write keys without the newline+indent
// decoration, producing compact output inside an indented document
// ({"x":10,"y":"y"} instead of {\n    "x": 10, ...}). Found via the
// benchmark PGO Full workload; fixed in interp.go's opFallback.
//
// Uses its own struct type: blueprint compilation is cached per type via
// sync.Once, so a type untouched by other tests compiles under the injected
// near-full pool here.

type kpOvfIndentStruct struct {
	KpOvfIndAlpha int     `json:"kp_ovf_ind_alpha"`
	KpOvfIndBeta  string  `json:"kp_ovf_ind_beta"`
	KpOvfIndGamma float64 `json:"kp_ovf_ind_gamma"`
	KpOvfIndInner struct {
		KpOvfIndX int    `json:"kp_ovf_ind_x"`
		KpOvfIndY string `json:"kp_ovf_ind_y"`
	} `json:"kp_ovf_ind_inner"`
}

func TestKeyPoolOverflow_MarshalIndent(t *testing.T) {
	if !encvm.Available {
		t.Skip("native encoder not available")
	}

	// Inject a near-full pool so every field of kpOvfIndentStruct overflows
	// addKey at Blueprint compile time and takes the Go fallback.
	restore := saveAndInjectKeyPool(65530)
	defer restore()

	v := kpOvfIndentStruct{
		KpOvfIndAlpha: 42,
		KpOvfIndBeta:  "hello",
		KpOvfIndGamma: 3.14,
	}
	v.KpOvfIndInner.KpOvfIndX = 10
	v.KpOvfIndInner.KpOvfIndY = "y"

	got, err := MarshalIndent(&v, "", "  ")
	if err != nil {
		t.Fatalf("MarshalIndent failed: %v", err)
	}

	want, err := json.MarshalIndent(&v, "", "  ")
	if err != nil {
		t.Fatalf("json.MarshalIndent failed: %v", err)
	}
	if string(got) != string(want) {
		t.Errorf("output mismatch under key pool overflow (indent):\n  vjson:  %q\n  stdlib: %q", got, want)
	}
}
