//go:build go1.27

package vjson

// On Go 1.27+, json.Marshal normalizes invalid UTF-8 and U+FFFD to the same
// raw bytes, so no version-specific differences are expected.
var goVersionExpectedDiff = []string{}
