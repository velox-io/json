//go:build darwin && arm64

package rsdec

import (
	"unsafe"

	"github.com/velox-io/json/ndec"
)

//go:noescape
//go:nosplit
func vjDecExec(ctx unsafe.Pointer)

//go:noescape
//go:nosplit
func vjDecResume(ctx unsafe.Pointer)

func init() {
	D = ndec.Driver{
		Available: true,
		Exec:      vjDecExec,
		Resume:    vjDecResume,
	}
}
