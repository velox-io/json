package vbind

import (
	"reflect"

	"github.com/velox-io/json/gort"
	"github.com/velox-io/json/rtcache"
	"github.com/velox-io/json/typ"
)

var typeTreeCache rtcache.Cache[*TypeTree]

// TypeTreeOf returns the process-wide canonical TypeTree for t. The returned
// tree is immutable and may be shared concurrently.
func TypeTreeOf(t reflect.Type) (*TypeTree, error) {
	rtp := uintptr(gort.TypePtr(t))
	return typeTreeCache.GetOrBuild(rtp, func() (*TypeTree, error) {
		ut := typ.UniTypeOf(t)
		if ut == nil {
			return nil, &uniTypeNilErr{t: t}
		}
		return Build(ut)
	})
}

// TypeTreeFor returns the process-wide canonical TypeTree for T.
func TypeTreeFor[T any]() (*TypeTree, error) {
	return TypeTreeOf(reflect.TypeFor[T]())
}

type uniTypeNilErr struct{ t reflect.Type }

func (e *uniTypeNilErr) Error() string {
	return "vbind: typ.UniTypeOf returned nil for " + e.t.String()
}
