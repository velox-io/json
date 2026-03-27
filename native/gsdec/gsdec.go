// Package gsdec provides the Go ↔ C bridge for the goto-based native JSON decoder.
package gsdec

import (
	"github.com/velox-io/json/ndec"
)

// D is the native C (goto) decoder driver. Populated by platform-specific init().
var D ndec.Driver
