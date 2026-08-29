//go:build vdec || vj_nondec

package vjson_test

// backendExpectedDivergences is empty for the vdec backend, which validates
// control characters in strings via its own scanner and has no fixed nesting
// depth limit. vdec is the opt-in backend (via -tags vdec) and the automatic
// fallback on platforms without the native ndec .syso; on supported platforms
// the default is the native ndec backend (see compat_ndec_test.go).
var backendGotPassingWantFailing = []string{}
var backendGotFailingWantPassing = []string{}
