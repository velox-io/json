//go:build arm64 && !vj_nolookup

package nativeref

import _ "embed"

//go:embed vlib_arm64.elf
var blobImage []byte
