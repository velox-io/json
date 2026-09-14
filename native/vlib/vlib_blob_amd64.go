//go:build amd64 && !vj_nolookup

package vlib

import _ "embed"

//go:embed vlib_amd64.elf
var blobImage []byte
