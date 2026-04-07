//go:build !vjgcstress

package venc

import (
	"unsafe"
)

func (es *encodeState) callvm(vmExec func(unsafe.Pointer), ctx *VjExecCtx) {
	vmExec(unsafe.Pointer(ctx))
}
