//go:build windows && !amd64

package ndec

import "unsafe"

// Windows builds without the native init file resolve the public Run
// wrappers and Available here: non-amd64 architectures.
// Callers gate on Available, so the panics stay unreached.
var Available bool

func DomParseCountedRun(ctx unsafe.Pointer) { panic("ndec: native decoder not linked") }

func DomBuildRun(ctx unsafe.Pointer) { panic("ndec: native decoder not linked") }

func BindParseRun(ctx unsafe.Pointer) { panic("ndec: native decoder not linked") }

func BindParseStreamRun(ctx unsafe.Pointer) { panic("ndec: native decoder not linked") }

func WindowScanRun(ctx unsafe.Pointer) { panic("ndec: native decoder not linked") }

func FmtParseRun(ctx unsafe.Pointer) { panic("ndec: native decoder not linked") }

func ValidRun(ctx unsafe.Pointer) { panic("ndec: native decoder not linked") }
