package pjson

import (
	"runtime"

	"github.com/penglei/pjson/jsonmarker"
)

func init() {
	var variant jsonmarker.Variant
	switch runtime.GOOS + "/" + runtime.GOARCH {
	case "darwin/arm64":
		variant = jsonmarker.VariantNeon
	case "linux/amd64":
		variant = jsonmarker.VariantSSE42
	default:
		panic("pjson: unsupported platform: " + runtime.GOOS + "/" + runtime.GOARCH)
	}
	DefaultScanner = jsonmarker.NewScanner(variant)
}

// Unmarshal parses JSON data into v.
// v must be a non-nil pointer.
func Unmarshal[T any](data []byte, v *T) error {
	p := NewParser(DefaultScanner)
	return p.Parse(data, v)
}
