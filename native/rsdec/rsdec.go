// Package rsdec provides the Go ↔ Rust bridge for the native JSON decoder.
//
// The package owns the compiled .syso object and the Plan9 assembly
// trampolines that translate Go calling convention to C ABI (Rust extern "C").
package rsdec

import (
	"github.com/velox-io/json/ndec"
)

// D is the native Rust decoder driver. Populated by platform-specific init().
var D ndec.Driver
