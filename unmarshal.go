package pjson

import (
	"runtime"

	"github.com/penglei/pjson/jsonmarker"
)

// DefaultPool is the process-wide parser pool initialized at startup.
var DefaultPool *ParserPool

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
	DefaultScanner = jsonmarker.NewStdScanner(variant)
	DefaultPool = NewParserPool(DefaultScanner)
}

// Unmarshal parses JSON data into v.
// v must be a non-nil pointer.
func Unmarshal[T any](data []byte, v *T) error {
	p := DefaultPool.Get(len(data))
	defer DefaultPool.Put(p)
	return p.Parse(data, v)
}
