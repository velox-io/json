//go:build amd64 && !vj_nolookup

package nativeref

import _ "embed"

//go:embed vlib_amd64.elf
var blobImage []byte
