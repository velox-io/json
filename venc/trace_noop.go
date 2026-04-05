//go:build !vjdebug

package venc

const vjTraceEnabled = false

// flushVMTrace is a no-op when vjdebug build tag is not set.
func (es *encodeState) flushVMTrace() {}

// setupVMTrace is a no-op when vjdebug build tag is not set.
func (es *encodeState) setupVMTrace() {} //nolint:unused

// traceRecordBlueprint is a no-op when vjdebug build tag is not set.
func (es *encodeState) traceRecordBlueprint(_ *Blueprint) {}

// traceFlushBlueprints is a no-op when vjdebug build tag is not set.
func (es *encodeState) traceFlushBlueprints() {}
