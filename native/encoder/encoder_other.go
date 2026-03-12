//go:build !(darwin && arm64) && !(linux && arm64) && !(linux && amd64)

package encoder

// Available stays false; vmExec stays nil.
// VMExec will panic if called (nil function pointer).
