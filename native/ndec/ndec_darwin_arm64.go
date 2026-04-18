//go:build darwin && arm64 && !vj_nondec

package ndec

import "unsafe"

//go:noescape
//go:nosplit
func vjNdecParseDefaultNeon(ctx unsafe.Pointer)

func init() {
	ndecParse = vjNdecParseDefaultNeon
	Available = true
}
