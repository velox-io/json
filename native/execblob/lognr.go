package execblob

import "runtime"

// LogWritePatch returns the loader patch that installs the running OS's
// write(2) syscall number into a blob's debug-log path (native/util/log.h).
// The arch-canonical blob is built for linux, so its baked default is only
// correct there; darwin numbers the syscall differently on each arch.
// Production builds never log, but the patched word is always present, so
// the patch is unconditional wherever a number is known. Where none exists
// (windows has no raw-syscall write contract for the blob), the blob keeps
// its build default and debug logging stays unavailable.
//
// Every blob whose EXTRA_SOURCES includes native/util/log.c needs this
// patch; blobs without the log path pass nil.
func LogWritePatch() []WordPatch {
	var nr uint64
	switch runtime.GOOS {
	case "linux":
		if runtime.GOARCH == "amd64" {
			nr = 1
		} else {
			nr = 64
		}
	case "darwin":
		if runtime.GOARCH == "amd64" {
			nr = 0x2000004
		} else {
			nr = 4
		}
	default:
		return nil
	}
	return []WordPatch{{Symbol: "vj_log_write_nr", Value: nr}}
}
