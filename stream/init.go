package stream

import (
	"reflect"
	"strings"

	"github.com/velox-io/json/typ"
)

func init() {
	// Register the predicate that tells typ "this reflect.Type is a
	// stream.Stream[T] instantiation" so buildUniType routes it to KindStream
	// instead of recursing into the struct payload.
	//
	// Match by definition-site PkgPath + generic Name prefix. reflect reports
	// stream.Stream[User] as PkgPath "github.com/velox-io/json/stream" and
	// Name "Stream[github.com/velox-io/json/...User]"; the "Stream[" prefix
	// distinguishes it from any other type in the same package.
	typ.RegisterStreamTypePredicate(func(t reflect.Type) bool {
		if t.PkgPath() != "github.com/velox-io/json/stream" {
			return false
		}
		name := t.Name()
		return strings.HasPrefix(name, "Stream[") || name == "Stream"
	})
}
