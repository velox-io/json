//go:build vj_nondec || !((darwin && arm64) || (linux && amd64) || (linux && arm64) || (windows && amd64))

package ndec

import "unsafe"

// These four stubs cover vj_nondec builds and platforms lacking a generated
// native object. Available remains false, directing callers to supported paths.
func vjNdecDOMParseCounted(ctx unsafe.Pointer) { panic("ndec: native decoder not linked") }

func vjNdecDOMBuild(ctx unsafe.Pointer) { panic("ndec: native decoder not linked") }

func vjNdecBindParse(ctx unsafe.Pointer) { panic("ndec: native decoder not linked") }

func vjNdecFmtParse(ctx unsafe.Pointer) { panic("ndec: native decoder not linked") }
