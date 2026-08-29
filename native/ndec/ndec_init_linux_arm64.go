//go:build linux && arm64 && !vj_nondec

package ndec

import "unsafe"

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

func init() {
	Available = true
}
