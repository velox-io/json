//go:build windows && amd64 && !vj_nondec

package ndec

import (
	"unsafe"
)

// Available reports whether native ndec entry points are linked for this build.
var Available bool

//go:noescape
//go:nosplit
func vjNdecDOMParseCounted(ctx unsafe.Pointer)

//go:noescape
//go:nosplit
func vjNdecDOMBuild(ctx unsafe.Pointer)

//go:noescape
//go:nosplit
func vjNdecBindParse(ctx unsafe.Pointer)

//go:noescape
//go:nosplit
func vjNdecFmtParse(ctx unsafe.Pointer)

// stackReserve grows the Go stack before entering the deepest native bind chain.
func stackReserve()

func init() {
	Available = true
}

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

func FmtParseRun(ctx unsafe.Pointer) {
	vjNdecFmtParse(ctx)
}
