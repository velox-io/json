//go:build !vjdebug

package vjson

const vjTraceEnabled = false

// flushVMTrace is a no-op when vjdebug build tag is not set.
func (m *Marshaler) flushVMTrace() {}

// setupVMTrace is a no-op when vjdebug build tag is not set.
func (m *Marshaler) setupVMTrace() {}

// traceRecordBlueprint is a no-op when vjdebug build tag is not set.
func (m *Marshaler) traceRecordBlueprint(_ *Blueprint) {}

// traceFlushBlueprints is a no-op when vjdebug build tag is not set.
func (m *Marshaler) traceFlushBlueprints() {}
