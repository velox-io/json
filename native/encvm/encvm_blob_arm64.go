//go:build arm64 && !vj_noencvm && !vj_encvmlink

package encvm

import _ "embed"

//go:embed encvm_arm64.elf
var blobImage []byte
