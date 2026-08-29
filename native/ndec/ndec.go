//go:build !windows

package ndec

import "unsafe"

// Available reports whether native ndec entry points are linked for this build.
var Available bool

func DomParseCountedRun(ctx unsafe.Pointer) { vjNdecDOMParseCounted(ctx) }

func DomBuildRun(ctx unsafe.Pointer) { vjNdecDOMBuild(ctx) }

func BindParseRun(ctx unsafe.Pointer) { vjNdecBindParse(ctx) }

func FmtParseRun(ctx unsafe.Pointer) { vjNdecFmtParse(ctx) }
