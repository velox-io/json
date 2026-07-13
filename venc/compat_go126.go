//go:build !go1.27

package venc

// On Go 1.26 and earlier, encoding/json replaces invalid UTF-8 with the
// \ufffd escape sequence. Match that behavior for stdcompat and UTF-8
// correction modes.

// EncRawUTF8Repl is zero on Go 1.26 and earlier: use \ufffd escape for
// invalid UTF-8, matching the pre-1.27 encoding/json behavior.
const EncRawUTF8Repl uint32 = 0

// invalidUTF8Repl is the replacement written by the Go fallback path
// (appendEscapedString) for each invalid UTF-8 byte.
var invalidUTF8Repl = []byte{'\\', 'u', 'f', 'f', 'f', 'd'}
