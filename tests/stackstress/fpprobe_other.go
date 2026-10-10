//go:build vjstackstress && !amd64

package stackstress

// haveFPProbe reports whether ucomisdFlags and stmxcsr read the hardware.
const haveFPProbe = false

func ucomisdFlags(a, b *[2]float64) (flagsF, flagsG uint64) { return 0, 0 }

func stmxcsr() uint32 { return 0 }
