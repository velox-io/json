//go:build go1.27

package jerr

import "encoding/json"

// bridgeUnmarshalTypeErrorErr copies the wrapped error into the stdlib
// UnmarshalTypeError, whose Err field exists from Go 1.27.
func bridgeUnmarshalTypeErrorErr(t *json.UnmarshalTypeError, err error) {
	t.Err = err
}
