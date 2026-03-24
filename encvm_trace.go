//go:build vjdebug

package vjson

import (
	"bytes"
	"fmt"
	"os"
	"sync"
	"unsafe"
)

const vjTraceEnabled = true

// vjTraceBufSize must match VJ_TRACE_BUF_SIZE in native/encvm/impl/types.h.
const vjTraceBufSize = 1048576

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

// fbReasonLabels maps fallback reason codes to human-readable labels.
// When trace output contains "YIELD(fb:N)", Go replaces it with "YIELD(<label>)".
var fbReasonLabels = [...]string{
	fbReasonUnknown:       "fallback",
	fbReasonMarshaler:     "marshaler",
	fbReasonTextMarshaler: "text_marshaler",
	fbReasonQuoted:        "quoted",
	fbReasonByteSlice:     "byte_slice",
	fbReasonByteArray:     "byte_array",
	fbReasonMapOmitempty:  "map_omitempty",
	fbReasonKeyPoolFull:   "keypool_full",
}

// expandFallbackReasons replaces any "YIELD(fb:N)" tokens in trace output
// with human-readable "YIELD(<reason>)" labels using fbReasonLabels.
func expandFallbackReasons(data []byte) []byte {
	prefix := []byte("YIELD(fb:")
	for {
		i := bytes.Index(data, prefix)
		if i < 0 {
			return data
		}
		// Find the closing ')' after the number.
		numStart := i + len(prefix)
		numEnd := numStart
		for numEnd < len(data) && data[numEnd] >= '0' && data[numEnd] <= '9' {
			numEnd++
		}
		if numEnd >= len(data) || data[numEnd] != ')' {
			// Malformed — skip past this occurrence to avoid infinite loop.
			data = data[numEnd:]
			continue
		}
		// Parse the number.
		var n int32
		for _, c := range data[numStart:numEnd] {
			n = n*10 + int32(c-'0')
		}
		// Look up the label.
		label := "fallback"
		if n >= 0 && int(n) < len(fbReasonLabels) && fbReasonLabels[n] != "" {
			label = fbReasonLabels[n]
		}
		// Replace "YIELD(fb:N)" with "YIELD(<label>)".
		replacement := []byte("YIELD(" + label + ")")
		data = append(data[:i], append(replacement, data[numEnd+1:]...)...)
	}
}

// addIndentGuides replaces one space in each two-space indent with a subtle
// guide marker, making deep nesting easier to align visually.
func addIndentGuides(data []byte) []byte {
	if len(data) == 0 {
		return data
	}

	const guide = "┆"
	guideBytes := []byte(guide)
	if os.Getenv("NO_COLOR") == "" {
		guideBytes = []byte("\x1b[2;90m" + guide + "\x1b[0m")
	}

	out := make([]byte, 0, len(data))
	for len(data) > 0 {
		line := data
		lineEnd := bytes.IndexByte(data, '\n')
		hasNewline := lineEnd >= 0
		if hasNewline {
			line = data[:lineEnd]
			data = data[lineEnd+1:]
		} else {
			data = data[:0]
		}

		indent := 0
		for indent < len(line) && line[indent] == ' ' {
			indent++
		}

		for i := 0; i+1 < indent; i += 2 {
			out = append(out, guideBytes...)
			out = append(out, ' ')
		}
		if indent%2 == 1 {
			out = append(out, ' ')
		}

		out = append(out, line[indent:]...)
		if hasNewline {
			out = append(out, '\n')
		}
	}

	return out
}

// flushVMTrace reads pending trace data from the ring buffer and prints
// it to stderr. Called after each VM exit (buffer full, yield, done, error).
func (m *marshaler) flushVMTrace() {
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

	// Post-process: expand fallback reasons and add indentation guides.
	out = expandFallbackReasons(out)
	out = addIndentGuides(out)

	fmt.Fprintf(os.Stderr, "####[vjson:trace] (%d bytes, %d total):\n%s",
		length, tb.Total, out)

	// Reset for next VM invocation.
	tb.Head = 0
	tb.Total = 0
}

// setupVMTrace sets up the trace buffer on the marshaler's VM context.
// Called from getMarshaler when vjdebug build tag is active.
func (m *marshaler) setupVMTrace() {
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

// traceBlueprints associates each marshaler with the blueprints collected
// during a single execVM call. Package-level to avoid adding fields to
// the marshaler struct (which is pooled and performance-sensitive).
var traceBlueprints sync.Map // *marshaler → *[]*Blueprint

// traceRecordBlueprint appends bp to the per-marshaler blueprint list,
// skipping duplicates (same pointer).
func (m *marshaler) traceRecordBlueprint(bp *Blueprint) {
	val, _ := traceBlueprints.LoadOrStore(m, &[]*Blueprint{})
	list := val.(*[]*Blueprint)
	for _, existing := range *list {
		if existing == bp {
			return
		}
	}
	*list = append(*list, bp)
}

// traceFlushBlueprints prints all collected blueprints for this marshaler
// to stderr, then removes the entry from the map.
func (m *marshaler) traceFlushBlueprints() {
	val, ok := traceBlueprints.LoadAndDelete(m)
	if !ok {
		return
	}
	list := val.(*[]*Blueprint)
	if len(*list) == 0 {
		return
	}
	for _, bp := range *list {
		dumpBlueprint(bp)
	}
}

// opcodeName maps opcode values to human-readable names.
var opcodeName = map[uint16]string{
	opBool:       "BOOL",
	opInt:        "INT",
	opInt8:       "INT8",
	opInt16:      "INT16",
	opInt32:      "INT32",
	opInt64:      "INT64",
	opUint:       "UINT",
	opUint8:      "UINT8",
	opUint16:     "UINT16",
	opUint32:     "UINT32",
	opUint64:     "UINT64",
	opFloat32:    "FLOAT32",
	opFloat64:    "FLOAT64",
	opString:     "STRING",
	opInterface:  "INTERFACE",
	opRawMessage: "RAW_MESSAGE",
	opNumber:     "NUMBER",
	opByteSlice:  "BYTE_SLICE",
	opSkipIfZero: "SKIP_IF_ZERO",
	opCall:       "CALL",
	opPtrDeref:   "PTR_DEREF",
	opPtrEnd:     "PTR_END",
	opSliceBegin: "SLICE_BEGIN",
	opSliceEnd:   "SLICE_END",
	opMap:        "MAP",
	opObjOpen:    "OBJ_OPEN",
	opObjClose:   "OBJ_CLOSE",
	opArrayBegin: "ARRAY_BEGIN",
	opMapStrStr:  "MAP_STR_STR",
	opRet:        "RET",
	opFallback:   "FALLBACK",
	opKString:    "KSTRING",
	opKInt:       "KINT",
	opKInt64:     "KINT64",
	opKQInt:      "KQINT",
	opKQInt64:    "KQINT64",
	opSeqFloat64: "SEQ_FLOAT64",
	opSeqInt:     "SEQ_INT",
	opSeqInt64:   "SEQ_INT64",
	opSeqString:  "SEQ_STRING",
	opMapStrIter:    "MAP_STR_ITER",
	opMapStrIterEnd: "MAP_STR_ITER_END",
	opTime:          "TIME",
}

// dumpBlueprint prints one blueprint's instruction listing to stderr
// with indentation that reflects structural nesting (OBJ_OPEN/CLOSE,
// SLICE_BEGIN/END, etc.), mirroring the trace output style.
// Line numbers stay left-aligned; only the opcode+key is indented.
func dumpBlueprint(bp *Blueprint) {
	name := bp.Name
	if name == "" {
		name = "<anonymous>"
	}

	// Build guide unit: "┆ " with optional dim color.
	guide := "┆ "
	if os.Getenv("NO_COLOR") == "" {
		guide = "\x1b[2;90m┆\x1b[0m "
	}

	var buf bytes.Buffer
	nOps := 0
	for pc := int32(0); pc < int32(len(bp.Ops)); pc += opSizeOf(opHdrAt(bp.Ops, pc).OpType) {
		nOps++
	}
	fmt.Fprintf(&buf, "####[vjson:blueprint] %s (%d ops, %d bytes):\n", name, nOps, len(bp.Ops))

	depth := 0
	for pc := int32(0); pc < int32(len(bp.Ops)); {
		hdr := opHdrAt(bp.Ops, pc)
		code := hdr.OpType
		sz := opSizeOf(code)

		label := opcodeName[code]
		if label == "" {
			label = fmt.Sprintf("OP_%d", code)
		}

		// Closing ops reduce depth before printing.
		switch code {
		case opObjClose, opSliceEnd, opPtrEnd, opMapStrIterEnd:
			depth--
			if depth < 0 {
				depth = 0
			}
		}

		// Byte offset left-aligned, then guide lines for depth.
		fmt.Fprintf(&buf, "%4d: ", pc)
		for j := 0; j < depth; j++ {
			buf.WriteString(guide)
		}
		sizeTag := "S"
		if opIsLong[hdr.OpType] {
			sizeTag = "L"
		}
		// Build annotation suffix (type name for OBJ_OPEN/CALL).
		ann := ""
		if bp.Annotations != nil {
			if a, ok := bp.Annotations[int(pc)]; ok {
				ann = " <" + a + ">"
			}
		}
		if code == opFallback && bp.Fallbacks != nil {
			if fb, ok := bp.Fallbacks[int(pc)]; ok {
				reason := "fallback"
				if int(fb.Reason) < len(fbReasonLabels) && fbReasonLabels[fb.Reason] != "" {
					reason = fbReasonLabels[fb.Reason]
				}
				if hdr.KeyLen > 0 {
					// Key stored in pool (normal fallback, e.g. marshaler/quoted).
					key := keyPoolBytes(hdr.KeyOff, hdr.KeyLen)
					fmt.Fprintf(&buf, "[%s] %s(%s) %s%s\n", sizeTag, label, reason, key, ann)
				} else if fb.TI != nil && fb.TI.Ext != nil && len(fb.TI.Ext.KeyBytes) > 0 {
					// Key NOT in pool (overflow); read from TypeInfo.
					fmt.Fprintf(&buf, "[%s] %s(%s) %s%s\n", sizeTag, label, reason, fb.TI.Ext.KeyBytes, ann)
				} else {
					fmt.Fprintf(&buf, "[%s] %s(%s)%s\n", sizeTag, label, reason, ann)
				}
			} else if hdr.KeyLen > 0 {
				key := keyPoolBytes(hdr.KeyOff, hdr.KeyLen)
				fmt.Fprintf(&buf, "[%s] %s %s%s\n", sizeTag, label, key, ann)
			} else {
				fmt.Fprintf(&buf, "[%s] %s%s\n", sizeTag, label, ann)
			}
		} else if hdr.KeyLen > 0 {
			key := keyPoolBytes(hdr.KeyOff, hdr.KeyLen)
			fmt.Fprintf(&buf, "[%s] %s %s%s\n", sizeTag, label, key, ann)
		} else {
			fmt.Fprintf(&buf, "[%s] %s%s\n", sizeTag, label, ann)
		}

		// Opening ops increase depth after printing.
		switch code {
		case opObjOpen, opSliceBegin, opArrayBegin, opPtrDeref, opMapStrIter:
			depth++
		}

		pc += sz
	}

	os.Stderr.Write(buf.Bytes())
}
