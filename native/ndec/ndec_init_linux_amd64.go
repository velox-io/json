//go:build linux && amd64

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

//go:noescape
//go:nosplit
func vjNdecBindParseStream(ctx unsafe.Pointer)

//go:noescape
//go:nosplit
func vjNdecWindowScan(ctx unsafe.Pointer)

//go:noescape
//go:nosplit
func vjNdecValid(ctx unsafe.Pointer)

func init() {
	Available = true
}
