//go:build vj_noencvm || !((darwin && arm64) || (linux && amd64) || (linux && arm64) || (windows && amd64))

package encvm

import "unsafe"

// Stub entries for builds without the native encoder VM: either the
// vj_noencvm tag is set or the platform has no compiled objects.
// Available stays false and VMExec must not be called.
func VMExec(ctx unsafe.Pointer) { panic("encvm: native encoder not linked") }

func VMExecFast(ctx unsafe.Pointer) { panic("encvm: native encoder not linked") }

func VMExecCompact(ctx unsafe.Pointer) { panic("encvm: native encoder not linked") }
