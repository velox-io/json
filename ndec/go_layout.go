package ndec

import "unsafe"

// goStringHeader is used only in unsafe paths that depend on the runtime
// string layout staying {Data, Len}.
type goStringHeader struct {
	data unsafe.Pointer
	len  uintptr
}

// goSliceHeader is used only where ndec needs direct slice header writes and
// therefore depends on the runtime layout staying {Data, Len, Cap}.
type goSliceHeader struct {
	data unsafe.Pointer
	len  int
	cap  int
}
