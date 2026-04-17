//go:build goexperiment.swissmap || go1.26

package venc

import (
	"reflect"
	"unsafe"

	"github.com/velox-io/json/gort"
)

var SwissMapLayoutOK = gort.SwissMapLayoutOK
var SwissMapStrIntLayoutOK = gort.SwissMapStrIntLayoutOK
var SwissMapStrInt64LayoutOK = gort.SwissMapStrInt64LayoutOK

// swissMapGlobalFlags holds the EncSplitGroup bit if the runtime uses
// KKKKVVVV group layout. OR'd into encodeState.flags at VM init time.
var swissMapGlobalFlags uint32

func init() {
	if gort.SwissMapSplitGroup {
		swissMapGlobalFlags = EncSplitGroup
	}
}

type mapsIter = gort.MapsIter

func mapsIterKey(it *mapsIter) unsafe.Pointer  { return gort.MapsIterKey(it) }
func mapsIterElem(it *mapsIter) unsafe.Pointer { return gort.MapsIterElem(it) }
func mapsIterInit(t unsafe.Pointer, m unsafe.Pointer, it *mapsIter) {
	gort.MapsIterInit(t, m, it)
}
func mapsIterNext(it *mapsIter) { gort.MapsIterNext(it) }

type GoMapIterator = gort.GoMapIterator

func probeSwissMapSlotSize(mapType reflect.Type, valSize uintptr) (slotSize uintptr, ok bool) {
	return gort.ProbeSwissMapSlotSize(mapType, valSize)
}
