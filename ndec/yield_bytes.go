// yield handler for []byte base64 decoding

package ndec

import (
	"encoding/base64"
	"fmt"
	"unsafe"
)

func (d *driverState) handleBase64Slice() error {
	raw := unsafe.Slice((*byte)(d.userData.RawPtr), d.userData.RawLen)
	decoded, err := base64.StdEncoding.DecodeString(string(raw))
	if err != nil {
		return fmt.Errorf("ndec: base64 decode %q: %w", raw, err)
	}

	frame, fi, err := d.pendingStructField("base64 slice")
	if err != nil {
		return err
	}
	if fi.Kind != uint8(bkSlice) {
		return fmt.Errorf("ndec: base64 yield on non-slice kind %d", fi.Kind)
	}

	sliceBT := (*typeInfo)(fi.Type)
	elemBT := sliceBT.elemTypeInfo()

	dstPtr := unsafe.Add(unsafe.Pointer(frame.BindDst), uintptr(fi.Offset))
	sh := (*goSliceHeader)(dstPtr)

	need := len(decoded)
	if need == 0 {
		sh.data = sliceBT.emptySliceData()
		sh.len = 0
		sh.cap = 0
	} else {
		var backing unsafe.Pointer
		if elemBT != nil && elemBT.elemHasPtr() {
			backing = unsafe.Pointer(unsafe.SliceData(make([]byte, need)))
		} else {
			backing = d.allocBacking(elemBT, need)
		}
		copy(unsafe.Slice((*byte)(backing), need), decoded)
		sh.data = backing
		sh.len = need
		sh.cap = need
	}

	frame.BindPendingFieldIdx = -1
	return nil
}
