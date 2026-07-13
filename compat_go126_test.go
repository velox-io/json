//go:build !go1.27

package vjson

// goVersionExpectedDiff lists JSONTestSuite cases where vjson and encoding/json
// decode to different Go values, but json.Marshal on Go 1.26 canonicalizes them
// to different bytes (because Go 1.26 emits \ufffd escape for invalid UTF-8,
// while valid U+FFFD passes through as raw bytes).
//
// On Go 1.27+, json.Marshal emits raw U+FFFD for both, so they canonicalize
// equally and the differences vanish.
var goVersionExpectedDiff = []string{
	"i_string_UTF-8_invalid_sequence.json",
	"i_string_UTF8_surrogate_U+D800.json",
	"i_string_invalid_utf-8.json",
	"i_string_iso_latin_1.json",
	"i_string_lone_utf8_continuation_byte.json",
	"i_string_not_in_unicode_range.json",
	"i_string_overlong_sequence_2_bytes.json",
	"i_string_overlong_sequence_6_bytes.json",
	"i_string_overlong_sequence_6_bytes_null.json",
	"i_string_truncated-utf-8.json",
}
