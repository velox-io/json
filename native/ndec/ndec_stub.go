//go:build vj_nondec || !(amd64 || arm64)

package ndec

import "unsafe"

// Available is false for vj_nondec builds and platforms without an
// arch-canonical blob: the stateless entries run their Go bodies and the
// binder takes its Go engine, so the stubs below are never reached.
const Available = false

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
