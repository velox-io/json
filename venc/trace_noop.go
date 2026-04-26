//go:build !vjdebug

package venc

const vjTraceEnabled = false

func (es *encodeState) flushVMTrace() {}

func (es *encodeState) setupVMTrace() {} //nolint:unused

func (es *encodeState) traceRecordBlueprint(_ *Blueprint) {}

func (es *encodeState) traceFlushBlueprints() {}
