//go:build vjstackstress

package stackstress

// haveFPProbe reports whether ucomisdFlags and stmxcsr read the hardware.
const haveFPProbe = true

// ucomisdFlags compares a and b element-wise with UCOMISD and returns the
// RFLAGS image captured right after each comparison.
//
//go:noescape
func ucomisdFlags(a, b *[2]float64) (flagsF, flagsG uint64)

// stmxcsr returns the calling thread's MXCSR.
func stmxcsr() uint32
