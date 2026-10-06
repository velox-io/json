//go:build !go1.27

package jerr

import "encoding/json"

// bridgeUnmarshalTypeErrorErr is a no-op before Go 1.27: the stdlib
// UnmarshalTypeError has no Err field to bridge into.
func bridgeUnmarshalTypeErrorErr(*json.UnmarshalTypeError, error) {}
