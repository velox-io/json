// Package ndec defines shared types for native JSON decoders (rsdec, gsdec).
package ndec

import "unsafe"

// DecExecCtx is the shared context between Go and native code.
// Layout must match the C/Rust DecExecCtx exactly (128 bytes).
type DecExecCtx struct {
	// Cache line 0: hot path
	SrcPtr     uintptr        // [0]  input buffer start
	SrcLen     uint32         // [8]  input buffer length
	Idx        uint32         // [12] current parse position
	CurBase    unsafe.Pointer // [16] current struct/array base address (GC-visible)
	TiPtr      uintptr        // [24] type descriptor (Go compiled)
	ExitCode   int32          // [32] exit/yield code
	Flags      uint32         // [36] option flags
	ScratchPtr uintptr        // [40] scratch buffer (Go allocated)
	ScratchCap uint32         // [48] scratch capacity
	ScratchLen uint32         // [52] scratch bytes used
	_pad56     uint32         // [56] reserved
	ErrDetail  uint32         // [60] error offset/subcode

	// Cache line 1: yield parameters + resume stack
	YieldParam0 uint64  // [64]
	YieldParam1 uint64  // [72]
	YieldParam2 uint64  // [80]
	ResumePtr   uintptr // [88] resume stack (Go allocated)
	ResumeCap   uint16  // [96] max frames
	ResumeDepth uint16  // [98] current depth
	_pad1       uint32  // [100]
	InsnPtr     uintptr // [104] instruction buffer
	InsnLen     uint32  // [112] instruction bytes written
	InsnCap     uint32  // [116] instruction buffer capacity
	_reserved   [8]byte // [120-127]
}

// Compile-time size assertion.
var _ [128]byte = [unsafe.Sizeof(DecExecCtx{})]byte{}

// Exit codes (terminal — native stops, Go reads result).
const (
	ExitOK            int32 = 0
	ExitUnexpectedEOF int32 = 1
	ExitSyntaxError   int32 = 2
	ExitTypeError     int32 = 3
)

// Yield codes (native pauses, Go acts, then resumes).
const (
	YieldString       int32 = 10
	YieldAllocSlice   int32 = 11
	YieldGrowSlice    int32 = 12
	YieldArenaFull    int32 = 13
	YieldMapInit      int32 = 14
	YieldMapAssign    int32 = 15
	YieldInsnFlush    int32 = 16
	YieldAllocPointer int32 = 17
	YieldFloatParse   int32 = 18 // gsdec only; rsdec never emits this
)

// Driver abstracts the native parser entry points.
type Driver struct {
	Available bool
	Exec      func(ctx unsafe.Pointer)
	Resume    func(ctx unsafe.Pointer)
}
