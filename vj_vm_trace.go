//go:build vjdebug

package vjson

import (
	"fmt"
	"os"
	"unsafe"
)

const vjTraceEnabled = true

// vjTraceBufSize must match VJ_TRACE_BUF_SIZE in native/encvm/impl/types.h.
const vjTraceBufSize = 16384

// VjTraceBuf mirrors the C VjTraceBuf layout.
type VjTraceBuf struct {
	Head  uint32
	Total uint32
	Data  [vjTraceBufSize]byte
}

// allocTraceBuf allocates a new trace ring buffer for the VM.
func allocTraceBuf() *VjTraceBuf {
	return new(VjTraceBuf)
}

// flushVMTrace reads pending trace data from the ring buffer and prints
// it to stderr. Called after each VM exit (buffer full, yield, done, error).
func (m *Marshaler) flushVMTrace() {
	if m.vmCtx.TraceBuf == nil {
		return
	}
	tb := (*VjTraceBuf)(m.vmCtx.TraceBuf)
	if tb.Total == 0 {
		return
	}

	// Calculate readable range.
	var start, length uint32
	if tb.Total <= vjTraceBufSize {
		// No overflow: all data is valid.
		start = 0
		length = tb.Head
	} else {
		// Overflow: oldest data starts at head (next write position).
		start = tb.Head
		length = vjTraceBufSize
	}

	// Read the ring buffer in order.
	out := make([]byte, 0, length)
	for i := uint32(0); i < length; i++ {
		idx := (start + i) & (vjTraceBufSize - 1)
		out = append(out, tb.Data[idx])
	}

	fmt.Fprintf(os.Stderr, "[vjson:trace] (%d bytes, %d total):\n%s",
		length, tb.Total, out)

	// Reset for next VM invocation.
	tb.Head = 0
	tb.Total = 0
}

// setupVMTrace sets up the trace buffer on the Marshaler's VM context.
// Called from getMarshaler when vjdebug build tag is active.
func (m *Marshaler) setupVMTrace() {
	if m.vmCtx.TraceBuf == nil {
		tb := allocTraceBuf()
		m.vmCtx.TraceBuf = unsafe.Pointer(tb)
	} else {
		// Reset existing buffer for reuse.
		tb := (*VjTraceBuf)(m.vmCtx.TraceBuf)
		tb.Head = 0
		tb.Total = 0
	}
}
