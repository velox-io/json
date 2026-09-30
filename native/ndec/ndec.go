package ndec

import "unsafe"

// The stateless entries (DOM, fmt, valid) run their pure-Go implementations
// when Available is false and write the same context. Available is a build
// constant, so each build compiles one side only. The bind and window
// entries are native-only; callers gate them on Available.

func DomParseCountedRun(ctx unsafe.Pointer) {
	if !Available {
		goDOMParseCounted((*DOMContext)(ctx))
		return
	}
	vjNdecDOMParseCounted(ctx)
}

func DomBuildRun(ctx unsafe.Pointer) {
	if !Available {
		goDOMBuild((*DOMContext)(ctx))
		return
	}
	vjNdecDOMBuild(ctx)
}

//go:noinline
func BindParseRun(ctx unsafe.Pointer) {
	stackReserve()
	vjNdecBindParse(ctx)
}

// BindParseStreamRun drives the streaming bind engine over one window at a
// time; the driver services BindYieldInput between calls.
//
//go:noinline
func BindParseStreamRun(ctx unsafe.Pointer) {
	stackReserve()
	vjNdecBindParseStream(ctx)
}

// WindowScanRun scans one input window and publishes its stable prefix.
func WindowScanRun(ctx unsafe.Pointer) {
	vjNdecWindowScan(ctx)
}

func FmtParseRun(ctx unsafe.Pointer) {
	if !Available {
		goFmtParse((*FmtContext)(ctx))
		return
	}
	vjNdecFmtParse(ctx)
}

// ValidRun validates one complete document.
func ValidRun(ctx unsafe.Pointer) {
	if !Available {
		goValid((*ValidContext)(ctx))
		return
	}
	vjNdecValid(ctx)
}
