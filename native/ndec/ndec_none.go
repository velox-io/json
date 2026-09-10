//go:build !((darwin && arm64) || (linux && amd64) || (linux && arm64) || (windows && amd64))

package ndec

import "unsafe"

func vjNdecDOMParseCounted(ctx unsafe.Pointer) { panic("ndec: native decoder not linked") }

func vjNdecDOMBuild(ctx unsafe.Pointer) { panic("ndec: native decoder not linked") }

func vjNdecBindParse(ctx unsafe.Pointer) { panic("ndec: native decoder not linked") }

func vjNdecBindParseStream(ctx unsafe.Pointer) { panic("ndec: native decoder not linked") }

func vjNdecWindowScan(ctx unsafe.Pointer) { panic("ndec: native decoder not linked") }

func vjNdecFmtParse(ctx unsafe.Pointer) { panic("ndec: native decoder not linked") }

func vjNdecValid(ctx unsafe.Pointer) { panic("ndec: native decoder not linked") }
