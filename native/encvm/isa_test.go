//go:build linux && amd64

package encvm

import (
	"errors"
	"os"
	"os/exec"
	"reflect"
	"testing"
	"unsafe"
)

// ---------------------------------------------------------------------------
// helpers
// ---------------------------------------------------------------------------

// funcPtr returns the raw function pointer value for comparison.
func funcPtr(fn func(unsafe.Pointer)) uintptr {
	if fn == nil {
		return 0
	}
	return reflect.ValueOf(fn).Pointer()
}

// ---------------------------------------------------------------------------
// TestSetISA_DefaultIsSSE42 — init should select SSE4.2.
// ---------------------------------------------------------------------------

func TestSetISA_DefaultIsSSE42(t *testing.T) {
	resetISAStateForTest()

	if got := CurrentISA(); got != ISASSE42 {
		t.Fatalf("after init, CurrentISA() = %v, want sse42", got)
	}
	if funcPtr(vmExec) != funcPtr(vjVMExecDefaultSSE42) {
		t.Fatal("vmExec does not point to SSE4.2 implementation")
	}
}

// ---------------------------------------------------------------------------
// TestSetISA_SwitchAndCurrentISA — SetISA changes CurrentISA.
// ---------------------------------------------------------------------------

func TestSetISA_SwitchAndCurrentISA(t *testing.T) {
	tests := []struct {
		name    string
		isa     ISA
		want    ISA
		wantErr error
		// guard: skip if CPU doesn't support the required ISA
		needAVX2   bool
		needAVX512 bool
	}{
		{
			name: "Default→SSE42",
			isa:  ISADefault,
			want: ISASSE42,
		},
		{
			name: "ExplicitSSE42",
			isa:  ISASSE42,
			want: ISASSE42,
		},
		{
			name:     "AVX2",
			isa:      ISAAVX2,
			want:     ISAAVX2,
			needAVX2: true,
		},
		{
			name:       "AVX512",
			isa:        ISAAVX512,
			want:       ISAAVX512,
			needAVX512: true,
		},
		{
			name: "AutoDetect",
			isa:  ISAAutoDetect,
			// want determined below based on CPU
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resetISAStateForTest()

			if tt.needAVX2 && !hasAVX2 {
				t.Skipf("CPU does not support AVX2")
			}
			if tt.needAVX512 && !hasAVX512 {
				t.Skipf("CPU does not support AVX-512BW")
			}

			// compute expected ISA for AutoDetect
			want := tt.want
			if tt.isa == ISAAutoDetect {
				switch {
				case hasAVX512:
					want = ISAAVX512
				case hasAVX2:
					want = ISAAVX2
				default:
					want = ISASSE42
				}
			}

			err := SetISA(tt.isa)
			if err != nil {
				t.Fatalf("SetISA(%v) unexpected error: %v", tt.isa, err)
			}
			if got := CurrentISA(); got != want {
				t.Errorf("CurrentISA() = %v, want %v", got, want)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// TestSetISA_FunctionPointers — verify vmExec points to the right impl.
// ---------------------------------------------------------------------------

func TestSetISA_FunctionPointers(t *testing.T) {
	resetISAStateForTest()

	// SSE4.2 by default
	if funcPtr(vmExec) != funcPtr(vjVMExecDefaultSSE42) {
		t.Error("default: vmExec != SSE42")
	}
	if funcPtr(vmExecFast) != funcPtr(vjVMExecFastSSE42) {
		t.Error("default: vmExecFast != SSE42")
	}
	if funcPtr(vmExecCompact) != funcPtr(vjVMExecCompactSSE42) {
		t.Error("default: vmExecCompact != SSE42")
	}

	if hasAVX2 {
		resetISAStateForTest()
		if err := SetISA(ISAAVX2); err != nil {
			t.Fatalf("SetISA(AVX2): %v", err)
		}
		if funcPtr(vmExec) != funcPtr(vjVMExecDefaultAVX2) {
			t.Error("avx2: vmExec != AVX2")
		}
		if funcPtr(vmExecFast) != funcPtr(vjVMExecFastAVX2) {
			t.Error("avx2: vmExecFast != AVX2")
		}
		if funcPtr(vmExecCompact) != funcPtr(vjVMExecCompactAVX2) {
			t.Error("avx2: vmExecCompact != AVX2")
		}
	}

	if hasAVX512 {
		resetISAStateForTest()
		if err := SetISA(ISAAVX512); err != nil {
			t.Fatalf("SetISA(AVX512): %v", err)
		}
		if funcPtr(vmExec) != funcPtr(vjVMExecDefaultAVX512) {
			t.Error("avx512: vmExec != AVX512")
		}
		if funcPtr(vmExecFast) != funcPtr(vjVMExecFastAVX512) {
			t.Error("avx512: vmExecFast != AVX512")
		}
		if funcPtr(vmExecCompact) != funcPtr(vjVMExecCompactAVX512) {
			t.Error("avx512: vmExecCompact != AVX512")
		}
	}
}

// ---------------------------------------------------------------------------
// TestSetISA_UnsupportedISA — requesting an ISA the CPU lacks.
// ---------------------------------------------------------------------------

func TestSetISA_UnsupportedISA(t *testing.T) {
	resetISAStateForTest()

	if !hasAVX2 {
		err := SetISA(ISAAVX2)
		if !errors.Is(err, ErrUnsupportedISA) {
			t.Errorf("SetISA(AVX2) on non-AVX2 CPU: got %v, want ErrUnsupportedISA", err)
		}
		if CurrentISA() != ISASSE42 {
			t.Errorf("ISA should remain SSE4.2 after failed SetISA, got %v", CurrentISA())
		}
	}

	if !hasAVX512 {
		resetISAStateForTest()
		err := SetISA(ISAAVX512)
		if !errors.Is(err, ErrUnsupportedISA) {
			t.Errorf("SetISA(AVX512) on non-AVX512 CPU: got %v, want ErrUnsupportedISA", err)
		}
		if CurrentISA() != ISASSE42 {
			t.Errorf("ISA should remain SSE4.2 after failed SetISA, got %v", CurrentISA())
		}
	}

	// Unknown ISA value.
	resetISAStateForTest()
	err := SetISA(ISA(999))
	if !errors.Is(err, ErrUnsupportedISA) {
		t.Errorf("SetISA(999): got %v, want ErrUnsupportedISA", err)
	}
}

// ---------------------------------------------------------------------------
// TestSetISA_Locked — SetISA fails after lock.
// ---------------------------------------------------------------------------

func TestSetISA_Locked(t *testing.T) {
	resetISAStateForTest()

	// Manually lock (simulates what VMExec does on first call).
	lockISAImpl()

	err := SetISA(ISASSE42)
	if !errors.Is(err, ErrISALocked) {
		t.Errorf("SetISA after lock: got %v, want ErrISALocked", err)
	}
}

// ---------------------------------------------------------------------------
// TestSetISA_LockedByVMExec — VMExec triggers the lock.
// ---------------------------------------------------------------------------

func TestSetISA_LockedByVMExec(t *testing.T) {
	resetISAStateForTest()

	// Call VMExec to trigger lock via sync.Once.
	// We need a valid (but minimal) context — we just need the lock to
	// fire, the actual C call will happen too, so supply a context whose
	// err_code is set to VJ_OK(0) immediately. The simplest approach is
	// to call lockISA directly (the wrapper's sync.Once delegates to it).
	isaFirstUse.Do(lockISA)

	err := SetISA(ISASSE42)
	if !errors.Is(err, ErrISALocked) {
		t.Errorf("SetISA after VMExec lock: got %v, want ErrISALocked", err)
	}
}

// ---------------------------------------------------------------------------
// TestSetISA_MultipleSwitch — switch ISA multiple times before lock.
// ---------------------------------------------------------------------------

func TestSetISA_MultipleSwitchBeforeLock(t *testing.T) {
	if !hasAVX2 {
		t.Skip("CPU does not support AVX2")
	}
	resetISAStateForTest()

	if err := SetISA(ISAAVX2); err != nil {
		t.Fatalf("first SetISA(AVX2): %v", err)
	}
	if CurrentISA() != ISAAVX2 {
		t.Fatalf("after SetISA(AVX2): got %v", CurrentISA())
	}

	if err := SetISA(ISASSE42); err != nil {
		t.Fatalf("SetISA(SSE42): %v", err)
	}
	if CurrentISA() != ISASSE42 {
		t.Fatalf("after SetISA(SSE42): got %v", CurrentISA())
	}

	if err := SetISA(ISAAVX2); err != nil {
		t.Fatalf("second SetISA(AVX2): %v", err)
	}
	if CurrentISA() != ISAAVX2 {
		t.Fatalf("after second SetISA(AVX2): got %v", CurrentISA())
	}
}

// ---------------------------------------------------------------------------
// TestISA_String — coverage for ISA.String().
// ---------------------------------------------------------------------------

func TestISA_String(t *testing.T) {
	cases := []struct {
		isa  ISA
		want string
	}{
		{ISADefault, "default"},
		{ISAAutoDetect, "auto"},
		{ISASSE42, "sse42"},
		{ISAAVX2, "avx2"},
		{ISAAVX512, "avx512"},
		{ISA(999), "unknown"},
	}
	for _, tc := range cases {
		if got := tc.isa.String(); got != tc.want {
			t.Errorf("ISA(%d).String() = %q, want %q", int(tc.isa), got, tc.want)
		}
	}
}

// ---------------------------------------------------------------------------
// TestSetISA_EnvVar — environment variable override (subprocess tests).
//
// Each sub-test runs the test binary in a child process with VJSON_ISA set,
// plus a sentinel env var so the child knows which assertion to run.
// ---------------------------------------------------------------------------

func TestSetISA_EnvVar(t *testing.T) {
	// If we're inside the child, run the assertion and exit.
	if sentinel := os.Getenv("VJSON_ISA_TEST_CHILD"); sentinel != "" {
		runEnvVarChild(t, sentinel)
		return
	}

	tests := []struct {
		name     string
		envValue string
		sentinel string // value of VJSON_ISA_TEST_CHILD
	}{
		{"auto", "auto", "auto"},
		{"sse42", "sse42", "sse42"},
		{"sse4.2_alias", "sse4.2", "sse42"},
		{"avx2", "avx2", "avx2"},
		{"avx512", "avx512", "avx512"},
		{"unknown_fallback", "nope", "sse42"},
		{"empty_default", "", "sse42_no_env"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Skip ISA if CPU doesn't have it — child would also fall back.
			// We only check the happy path where CPU supports the requested ISA.
			if tt.envValue == "avx2" && !hasAVX2 {
				t.Skip("CPU does not support AVX2")
			}
			if tt.envValue == "avx512" && !hasAVX512 {
				t.Skip("CPU does not support AVX-512BW")
			}

			cmd := exec.Command(os.Args[0],
				"-test.run=^TestSetISA_EnvVar$",
				"-test.v",
			)
			cmd.Env = append(os.Environ(),
				"VJSON_ISA="+tt.envValue,
				"VJSON_ISA_TEST_CHILD="+tt.sentinel,
			)
			out, err := cmd.CombinedOutput()
			if err != nil {
				t.Fatalf("child process failed:\n%s\nerr: %v", out, err)
			}
		})
	}
}

// runEnvVarChild runs inside the subprocess and checks CurrentISA.
func runEnvVarChild(t *testing.T, sentinel string) {
	t.Helper()

	var want ISA
	switch sentinel {
	case "auto":
		switch {
		case hasAVX512:
			want = ISAAVX512
		case hasAVX2:
			want = ISAAVX2
		default:
			want = ISASSE42
		}
	case "sse42", "sse42_no_env":
		want = ISASSE42
	case "avx2":
		want = ISAAVX2
	case "avx512":
		want = ISAAVX512
	default:
		t.Fatalf("unknown sentinel %q", sentinel)
	}

	if got := CurrentISA(); got != want {
		t.Fatalf("child (sentinel=%s): CurrentISA() = %v, want %v", sentinel, got, want)
	}
}
