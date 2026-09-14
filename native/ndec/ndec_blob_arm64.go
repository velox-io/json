//go:build arm64 && !vj_nondec && !vj_ndeclink

package ndec

import _ "embed"

//go:embed ndec_arm64.elf
var blobImage []byte
