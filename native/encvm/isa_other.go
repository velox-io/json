//go:build !amd64

package encvm

// setISAImpl is a no-op on non-amd64 platforms (no ISA selection available).
func setISAImpl(_ ISA) error { return nil }

// lockISAImpl is a no-op on non-amd64 platforms.
func lockISAImpl() {}
