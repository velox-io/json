//go:build vjgcstress

package venc

import (
	"fmt"
	"runtime"
	"unsafe"
)

func (es *encodeState) StackExpand(v int) {
	es.stackExpandRecur(v, 128)
}

//go:noinline
func (es *encodeState) stackExpandRecur(v int, depth int) {
	var x [128]int
	x[0] = v
	if depth > 0 {
		es.stackExpandRecur(v, depth-1)
	}
	if x[0] < 0 {
		fmt.Println(x[0])
	}
}

func (es *encodeState) callvm(vmExec func(unsafe.Pointer), ctx *VjExecCtx) {
	runtime.GC()
	vmExec(unsafe.Pointer(ctx))
}
