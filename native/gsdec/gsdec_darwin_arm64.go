//go:build darwin && arm64

package gsdec

import (
	"unsafe"

	"github.com/velox-io/json/ndec"
)

//go:noescape
//go:nosplit
func vjGdecExec(ctx unsafe.Pointer)

//go:noescape
//go:nosplit
func vjGdecResume(ctx unsafe.Pointer)

func init() {
	D = ndec.Driver{
		Available: true,
		Exec:      vjGdecExec,
		Resume:    vjGdecResume,
	}
}
