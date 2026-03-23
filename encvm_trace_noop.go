//go:build !vjdebug

package vjson

const vjTraceEnabled = false

// flushVMTrace is a no-op when vjdebug build tag is not set.
func (m *marshaler) flushVMTrace() {}

// setupVMTrace is a no-op when vjdebug build tag is not set.
func (m *marshaler) setupVMTrace() {}

// traceRecordBlueprint is a no-op when vjdebug build tag is not set.
func (m *marshaler) traceRecordBlueprint(_ *Blueprint) {}

// traceFlushBlueprints is a no-op when vjdebug build tag is not set.
func (m *marshaler) traceFlushBlueprints() {}
