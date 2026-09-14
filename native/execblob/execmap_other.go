//go:build !darwin && !linux && !windows

package execblob

import "errors"

var errUnsupported = errors.New("execblob: no executable memory support on this OS")

func writeExec([]byte, []wordAt) (uintptr, error) { return 0, errUnsupported }
