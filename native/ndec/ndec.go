// Package ndec provides the Go ↔ C bridge for the native JSON decoder.
//
// The package owns the compiled .syso objects and Plan9 assembly trampolines
// that translate Go calling convention to C ABI.
//
// Unlike encvm which has full/compact/fast modes, ndec currently has a single
// "default" mode (no preprocessor variants). The exported entry is named
// ParseDefault to keep room for additional modes (e.g. streaming) later.
package ndec

import "unsafe"

// Available reports whether the native C decoder is linked on this platform.
var Available bool

var ndecParse func(ctx unsafe.Pointer)

func ParseDefault(ctx unsafe.Pointer) { ndecParse(ctx) }
