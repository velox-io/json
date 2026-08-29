//go:build vjpgoinstr

// Instrumentation-PGO profile flush (cgo bridge).
//
// The syso is compiled with -fprofile-instr-generate for instrumentation PGO
// collection. Its per-block counters live in the __llvm_prf_cnts section of
// the test binary and are normally flushed to LLVM_PROFILE_FILE by a C atexit
// handler. A Go test binary exits through the Go runtime, which does NOT run C
// atexit handlers, so without an explicit flush the .profraw ends up 0 bytes.
//
// vjProfileFlush wraps __llvm_profile_initialize + __llvm_profile_write_file
// (both from libclang_rt.profile, linked in via -ldflags
// "-extldflags=-fprofile-instr-generate"). It is called from TestMain in
// pgo_instr_testmain_test.go.
//
// __llvm_profile_initialize reads LLVM_PROFILE_FILE into the runtime's
// filename state. Normally a C constructor does this at startup, but a Go
// binary on windows never runs CRT constructors, so the initialize call is
// required for the env var to take effect. On ELF/Mach-O the constructor has
// already parsed the same pattern and the call returns early.
//
// cgo is not allowed in _test.go files, so the C bridge lives here in a regular
// (build-tagged) source file. Guarded by the `vjpgoinstr` build tag so normal
// builds never compile it.
package benchmark

/*
void __llvm_profile_initialize(void);
int __llvm_profile_write_file(void);
*/
import "C"

func vjProfileFlush() {
	C.__llvm_profile_initialize()
	C.__llvm_profile_write_file()
}
