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

type mapsIter = gort.MapsIter

func mapsIterKey(it *mapsIter) unsafe.Pointer  { return gort.MapsIterKey(it) }
func mapsIterElem(it *mapsIter) unsafe.Pointer { return gort.MapsIterElem(it) }
func mapsIterInit(t unsafe.Pointer, m unsafe.Pointer, it *mapsIter) {
	gort.MapsIterInit(t, m, it)
}
func mapsIterNext(it *mapsIter) { gort.MapsIterNext(it) }

type GoMapIterator = gort.GoMapIterator

// func mapiterinit(t unsafe.Pointer, m unsafe.Pointer, it *GoMapIterator) {
// 	gort.Mapiterinit(t, m, it)
// }
//
// func mapiternext(it *GoMapIterator) {
// 	gort.Mapiternext(it)
// }

func probeSwissMapSlotSize(mapType reflect.Type, valSize uintptr) (slotSize uintptr, ok bool) {
	return gort.ProbeSwissMapSlotSize(mapType, valSize)
}
