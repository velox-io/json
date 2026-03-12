//go:build !vjdebug

package vjson

// flushVMTrace is a no-op when vjdebug build tag is not set.
func (m *Marshaler) flushVMTrace() {}

// setupVMTrace is a no-op when vjdebug build tag is not set.
func (m *Marshaler) setupVMTrace() {}
