//go:build arm64 && !vj_nolookup

package vlib

import _ "embed"

//go:embed vlib_arm64.elf
var blobImage []byte
