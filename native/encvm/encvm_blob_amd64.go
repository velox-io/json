//go:build amd64 && !vj_noencvm && !vj_encvmlink

package encvm

import _ "embed"

//go:embed encvm_amd64.elf
var blobImage []byte
