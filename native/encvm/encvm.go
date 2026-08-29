package encvm

// Available reports whether the native encoder VM is linked for this
// platform. The VMExec entries live in the per-platform init files:
// windows grows the goroutine stack first (stackReserve), the other
// platforms forward straight to the NOSPLIT trampoline.
var Available bool
