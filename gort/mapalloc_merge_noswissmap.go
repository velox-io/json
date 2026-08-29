//go:build !goexperiment.swissmap && !go1.26

package gort

import (
	"reflect"
	"unsafe"
)

// compositeMapType is a stub for non-swissmap builds. The composite path
// requires the real GroupType from abi.MapType, which is unavailable here;
// PlanMapSlots returns the lazy plan (MapAllocUnit only) instead.
func compositeMapType(_ unsafe.Pointer) (typ reflect.Type, groupOff, stride uintptr, ok bool) {
	return nil, 0, 0, false
}
