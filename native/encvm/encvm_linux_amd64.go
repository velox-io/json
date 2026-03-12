//go:build linux && amd64

package encvm

import (
	"os"
	"strings"
	"unsafe"

	"golang.org/x/sys/cpu"
)

// CPU feature flags cached at package init time.
var (
	hasAVX2   = cpu.X86.HasAVX2
	hasAVX512 = cpu.X86.HasAVX512BW
)

// ---- Default mode ----

//go:noescape
//go:nosplit
func vjVMExecDefaultSSE42(ctx unsafe.Pointer)

//go:noescape
//go:nosplit
func vjVMExecDefaultAVX2(ctx unsafe.Pointer)

//go:noescape
//go:nosplit
func vjVMExecDefaultAVX512(ctx unsafe.Pointer)

// ---- Fast mode ----

//go:noescape
//go:nosplit
func vjVMExecFastSSE42(ctx unsafe.Pointer)

//go:noescape
//go:nosplit
func vjVMExecFastAVX2(ctx unsafe.Pointer)

//go:noescape
//go:nosplit
func vjVMExecFastAVX512(ctx unsafe.Pointer)

// ---- Compact mode ----

//go:noescape
//go:nosplit
func vjVMExecCompactSSE42(ctx unsafe.Pointer)

//go:noescape
//go:nosplit
func vjVMExecCompactAVX2(ctx unsafe.Pointer)

//go:noescape
//go:nosplit
func vjVMExecCompactAVX512(ctx unsafe.Pointer)

// applySSE42 sets the VM function pointers to SSE4.2 implementations.
func applySSE42() {
	vmExec = vjVMExecDefaultSSE42
	vmExecFast = vjVMExecFastSSE42
	vmExecCompact = vjVMExecCompactSSE42
	currentISA = ISASSE42
}

// applyAVX2 sets the VM function pointers to AVX2 implementations.
func applyAVX2() {
	vmExec = vjVMExecDefaultAVX2
	vmExecFast = vjVMExecFastAVX2
	vmExecCompact = vjVMExecCompactAVX2
	currentISA = ISAAVX2
}

// applyAVX512 sets the VM function pointers to AVX-512 implementations.
func applyAVX512() {
	vmExec = vjVMExecDefaultAVX512
	vmExecFast = vjVMExecFastAVX512
	vmExecCompact = vjVMExecCompactAVX512
	currentISA = ISAAVX512
}

// applyAutoDetect selects the best ISA available on this CPU.
func applyAutoDetect() {
	if hasAVX512 {
		applyAVX512()
	} else if hasAVX2 {
		applyAVX2()
	} else {
		applySSE42()
	}
}

func init() {
	// Environment variable takes precedence over the compiled-in default.
	if envISA := os.Getenv("VJSON_ISA"); envISA != "" {
		switch strings.ToLower(envISA) {
		case "auto":
			applyAutoDetect()
		case "sse42", "sse4.2":
			applySSE42()
		case "avx2":
			if hasAVX2 {
				applyAVX2()
			} else {
				applySSE42()
			}
		case "avx512":
			if hasAVX512 {
				applyAVX512()
			} else {
				applySSE42()
			}
		default:
			applySSE42()
		}
	} else {
		// Default: SSE4.2 — provides the most consistent performance.
		// Users who want AVX2/AVX-512 must opt in via SetISA or VJSON_ISA.
		applySSE42()
	}
	Available = true
}
