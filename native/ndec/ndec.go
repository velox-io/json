//go:build !windows

package ndec

import "unsafe"

// Available reports whether native ndec entry points are linked for this build.
var Available bool

func DomParseCountedRun(ctx unsafe.Pointer) { vjNdecDOMParseCounted(ctx) }

func DomBuildRun(ctx unsafe.Pointer) { vjNdecDOMBuild(ctx) }

func BindParseRun(ctx unsafe.Pointer) { vjNdecBindParse(ctx) }

// BindParseStreamRun drives the streaming bind engine over one window at a
// time; the driver services BindYieldInput between calls.
func BindParseStreamRun(ctx unsafe.Pointer) { vjNdecBindParseStream(ctx) }

// WindowScanRun scans one input window and publishes its stable prefix.
func WindowScanRun(ctx unsafe.Pointer) { vjNdecWindowScan(ctx) }

func FmtParseRun(ctx unsafe.Pointer) { vjNdecFmtParse(ctx) }

// ValidRun validates one complete document; the context's Err field holds
// the verdict.
func ValidRun(ctx unsafe.Pointer) { vjNdecValid(ctx) }
