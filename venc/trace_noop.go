//go:build !vjdebug

package venc

const vjTraceEnabled = false

// flushVMTrace is a no-op when vjdebug build tag is not set.
func (m *marshaler) flushVMTrace() {}

// setupVMTrace is a no-op when vjdebug build tag is not set.
func (m *marshaler) setupVMTrace() {} //nolint:unused

// traceRecordBlueprint is a no-op when vjdebug build tag is not set.
func (m *marshaler) traceRecordBlueprint(_ *Blueprint) {}

// traceFlushBlueprints is a no-op when vjdebug build tag is not set.
func (m *marshaler) traceFlushBlueprints() {}
