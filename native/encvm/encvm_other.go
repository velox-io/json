//go:build !(darwin && arm64) && !(linux && arm64) && !(linux && amd64)

package encvm

// Available stays false; vmExec stays nil.
// VMExec will panic if called (nil function pointer).
