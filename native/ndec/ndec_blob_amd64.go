//go:build amd64 && !vj_nondec && !vj_ndeclink

package ndec

import _ "embed"

//go:embed ndec_amd64.elf
var blobImage []byte
