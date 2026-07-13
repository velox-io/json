//go:build go1.27

package venc

// Go 1.27 backs encoding/json with json/v2, which replaces invalid UTF-8
// with the raw U+FFFD character (UTF-8 bytes ef bf bd) instead of the
// \ufffd escape sequence. Match that behavior for stdcompat and UTF-8
// correction modes.

// EncRawUTF8Repl (bit 5) signals the native VM to emit raw U+FFFD bytes
// for invalid UTF-8 instead of the \ufffd escape. Bit 5 mirrors
// VJ_FLAGS_RAW_UTF8_REPLACEMENT in native/encvm/impl/types.h.
const EncRawUTF8Repl uint32 = 1 << 5

// invalidUTF8Repl is the replacement written by the Go fallback path
// (appendEscapedString) for each invalid UTF-8 byte.
var invalidUTF8Repl = []byte{0xef, 0xbf, 0xbd}
