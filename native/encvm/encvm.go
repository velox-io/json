// Package encvm provides the Go ↔ C bridge for the native JSON encoder VM.
//
// The package owns the compiled .syso object (vj_vm_exec) and the
// Plan9 assembly trampolines that translate Go calling convention to C ABI.
//
// The root vjson package sets up a VjExecCtx and calls VMExec()
// with an unsafe.Pointer to it. This package never interprets the context
// struct — layout correctness is enforced by compile-time assertions in
// both C (native/impl/encoder_types.h) and Go (vj_native_encoder.go).
//
// # ISA Selection (amd64 only)
//
// On amd64, the native encoder supports multiple instruction set levels:
// SSE4.2, AVX2, and AVX-512. By default, SSE4.2 is used for its consistent
// performance characteristics.
//
// To enable AVX2 or AVX-512, call [SetISA] before any Marshal/Encode operations:
//
//	import "github.com/velox-io/json/native/encvm"
//
//	func init() {
//	    encvm.SetISA(encvm.ISAAVX2)       // enable AVX2
//	    encvm.SetISA(encvm.ISAAutoDetect)  // original auto-detection
//	}
//
// Alternatively, set the VJSON_ISA environment variable:
//
//	VJSON_ISA=avx2 ./myprogram
//	VJSON_ISA=auto ./myprogram
//
// Valid values: auto, sse42, avx2, avx512. ISA selection is locked after
// the first VMExec call; subsequent [SetISA] calls return [ErrISALocked].
package encvm

import (
	"errors"
	"sync"
	"unsafe"
)

// ISA represents an instruction set architecture level for the native encoder.
type ISA int

const (
	// ISADefault selects the library default (SSE4.2 on amd64, NEON on arm64).
	ISADefault ISA = iota
	// ISAAutoDetect selects the best available ISA based on runtime CPU
	// feature detection (the pre-v0.x behavior: AVX-512 > AVX2 > SSE4.2).
	ISAAutoDetect
	// ISASSE42 forces SSE4.2 on amd64.
	ISASSE42
	// ISAAVX2 forces AVX2 on amd64. Returns [ErrUnsupportedISA] if the CPU
	// does not support AVX2.
	ISAAVX2
	// ISAAVX512 forces AVX-512 on amd64. Returns [ErrUnsupportedISA] if the
	// CPU does not support AVX-512BW.
	ISAAVX512
)

// String returns the human-readable name of the ISA level.
func (isa ISA) String() string {
	switch isa {
	case ISADefault:
		return "default"
	case ISAAutoDetect:
		return "auto"
	case ISASSE42:
		return "sse42"
	case ISAAVX2:
		return "avx2"
	case ISAAVX512:
		return "avx512"
	default:
		return "unknown"
	}
}

var (
	// ErrUnsupportedISA is returned by [SetISA] when the requested ISA is
	// not supported by the current CPU.
	ErrUnsupportedISA = errors.New("encvm: requested ISA not supported by CPU")
	// ErrISALocked is returned by [SetISA] when called after the first
	// VMExec invocation (ISA is frozen once encoding has started).
	ErrISALocked = errors.New("encvm: ISA selection locked after first use")
)

// currentISA records the ISA level that is currently active.
var currentISA ISA

// SetISA configures the instruction set used by the native encoder.
// Must be called before the first Marshal / Encode call; otherwise it
// returns [ErrISALocked].
//
// On non-amd64 platforms this is a no-op and always returns nil.
func SetISA(isa ISA) error {
	return setISAImpl(isa)
}

// CurrentISA returns the ISA level currently in effect.
func CurrentISA() ISA {
	return currentISA
}

// Available reports whether the native C encoder is linked on this platform.
// Set to true by platform-specific init() when at least one ISA is available.
var Available bool

// vmExec holds the default-mode ISA-specific entry point selected at init time.
var vmExec func(ctx unsafe.Pointer)

// vmExecFast holds the fast-mode ISA-specific entry point selected at init time.
// Fast mode unconditionally uses the fast string escape path (no HTML/UTF-8/
// line-terminator checks), eliminating runtime flag dispatch.
var vmExecFast func(ctx unsafe.Pointer)

// vmExecCompact holds the compact-mode ISA-specific entry point selected at init time.
// Compact mode has all indent code paths eliminated at compile time (indent_step=0),
// but retains runtime string escape flag dispatch.
var vmExecCompact func(ctx unsafe.Pointer)

// isaFirstUse locks ISA selection after the first VMExec call.
var isaFirstUse sync.Once

// lockISA freezes the ISA so that subsequent SetISA calls fail.
// Platform files (isa_amd64.go) implement the actual locking.
func lockISA() {
	lockISAImpl()
}

// VMExec calls the default-mode native encoder entry point.
// The first call locks the ISA level; subsequent SetISA calls will
// return [ErrISALocked].
func VMExec(ctx unsafe.Pointer) {
	isaFirstUse.Do(lockISA)
	vmExec(ctx)
}

// VMExecFast calls the fast-mode native encoder entry point.
func VMExecFast(ctx unsafe.Pointer) {
	isaFirstUse.Do(lockISA)
	vmExecFast(ctx)
}

// VMExecCompact calls the compact-mode native encoder entry point.
func VMExecCompact(ctx unsafe.Pointer) {
	isaFirstUse.Do(lockISA)
	vmExecCompact(ctx)
}
