package ndec

import "unsafe"

// stackReserve grows the Go stack before entering the deepest native bind chain.
func stackReserve()

func DomParseCountedRun(ctx unsafe.Pointer) {
	vjNdecDOMParseCounted(ctx)
}

func DomBuildRun(ctx unsafe.Pointer) {
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
	vjNdecFmtParse(ctx)
}

// ValidRun validates one complete document through the native entry.
func ValidRun(ctx unsafe.Pointer) {
	vjNdecValid(ctx)
}
