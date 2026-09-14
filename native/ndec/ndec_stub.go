//go:build vj_nondec || !(amd64 || arm64)

package ndec

import "unsafe"

// These stubs cover vj_nondec builds and platforms without an arch-canonical
// blob (any OS on an arch the blob was never built for, or an OS whose
// executable-mapping policy refused the blob: execblob.Load failure fails
// init instead). Callers take the supported Go paths.
func vjNdecDOMParseCounted(ctx unsafe.Pointer) { panic("ndec: native decoder not linked") }

func vjNdecDOMBuild(ctx unsafe.Pointer) { panic("ndec: native decoder not linked") }

func vjNdecBindParse(ctx unsafe.Pointer) { panic("ndec: native decoder not linked") }

func vjNdecBindParseStream(ctx unsafe.Pointer) { panic("ndec: native decoder not linked") }

func vjNdecWindowScan(ctx unsafe.Pointer) { panic("ndec: native decoder not linked") }

func vjNdecFmtParse(ctx unsafe.Pointer) { panic("ndec: native decoder not linked") }

func vjNdecValid(ctx unsafe.Pointer) { panic("ndec: native decoder not linked") }

// stackReserve keeps ndec.go's wrapper code identical across platforms; the
// panic stubs above are never reached, so there is no native chain to reserve
// for.
func stackReserve() {}
