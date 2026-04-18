// Native parser driver loop and yield dispatch.
//
// The driver manages pooling of ndecCtx + bindUserData, boots the native
// parser via trampoline, and dispatches yield actions (GC allocation, error
// rendering) when the parser suspends.
//
// Address safety: parser cursors that may point to one-past-end sentinels
// use uintptr (not unsafe.Pointer) to avoid keeping GC-visible pointers
// outside heap bounds. The driver holds input and itself alive via
// runtime.KeepAlive across native calls.
//
// Stack merging: all frame state lives on ctx.Frames[].Bind*, sharing
// ctx.Depth with the parser.
//
// Scratch protocol: the driver pre-allocates scratch (cap = len(input)) and
// writes its base pointer + cap into userData. The C reactor maintains
// ScratchLen for escaped-string paths, appending unescape results into
// [scratch_ptr, scratch_ptr + scratch_len).

package ndec

import (
	"runtime"
	"sync"
	"sync/atomic"
	"unsafe"

	nativendec "github.com/velox-io/json/native/ndec"
)

// phaseRootValue / phaseRootDone mirror NdecPhase enum from native types.h.
const (
	phaseRootValue uint32 = 0
	phaseRootDone  uint32 = 7
)
const atofCtxSize = 1976

type driverState struct {
	// ctx must be at offset 0.
	ctx ndecCtx

	// userData follows ctx; reactor dereferences it via ctx.UserData.
	userData bindUserData

	// scratch pool: destination for C-side unescape results.
	scratch []byte

	// slab is a merged arena backing noscan slices. Lazy-allocated on the
	// first noscan grow (capacity bounded by len(input), clamped by
	// slabCapMax; see ensureSlab). Subsequent noscan grows bump-allocate
	// from slab, consolidating N individual make([]byte) calls into one
	// mallocgc.
	//
	// Critical invariant: once allocated, slab capacity never grows within
	// a single Unmarshal call (reallocation would invalidate dispatched
	// hdr.data pointers). When remaining space is insufficient, individual
	// make([]byte) calls serve as fallback while slab continues to serve
	// subsequent small requests.
	//
	// Lifecycle: release sets d.slab = nil to drop the driver's reference.
	// The caller-held slice headers indirectly reference the backing array,
	// and GC traces through the hdr.data unsafe.Pointer to the underlying
	// array (even for noscan arrays).
	slab []byte

	// kvBuf is the KV buffer region for MAP frames. Multiple live MAP
	// frames share it via region bump allocation.
	//
	// Per-frame layout: a [base, base + kv_buf_cap*kvSlotSize) sub-region.
	// base is carved out by driver on BEGIN_MAP yield and written to
	// frame.BindSliceHdr (MAP frames reuse the SLICE hdr slot). C-side
	// hooks yield FLUSH_MAP when the buffer is full (kv_count == kv_buf_cap)
	// or when end_object closes the map; driver then iterates the region,
	// calling mapassign + typedmemmove in bulk.
	//
	// Capacity grows lazily on demand. Under nested maps, a grow may
	// relocate the backing array, so all live MAP frames' BindSliceHdr
	// pointers must be rebased to the new backing.
	//
	// String header data pointers within a slot reference input or scratch
	// (both kept alive by the driver). After mapassign writes them into
	// the map, GC tracks them through hmap/swissmap type metadata.
	kvBuf []byte

	// atofCtx is the C-side atof_ctx storage slot (1976 B, reused across
	// number fields). driverPool.New sets userData.AtofCtx to &atofCtx[0]
	// once; neither reset nor runUnmarshal touch it. atof internals are
	// write-then-read, so no clearing is needed.
	//
	// Placed at the end for three reasons:
	//   1. Go never reads this memory (C-only). It is the coldest field in
	//      driverState; pushing it to the end keeps ctx/userData/scratch
	//      (the three hot fields) together in cache lines.
	//   2. [N]byte is noscan, adding no GC tracking cost.
	//   3. Embedded array (not *atof_ctx pointer) avoids an extra alloc
	//      and keeps the address pooled with driverState for zero-overhead
	//      L1 fast-pool hits.
	atofCtx [atofCtxSize]byte

	// rootBT is written at runUnmarshal entry for error-path field name
	// and type resolution (makeTypeError => renderFieldPath).
	rootBT *typeInfo
}

// Two-tier pool: fast atomic cache bypasses sync.Pool on the single-thread
// hot path.
var (
	poolFast   atomic.Pointer[driverState]
	driverPool = sync.Pool{
		New: func() any {
			d := &driverState{}
			// userData.AtofCtx is invariant across calls: atofCtx array
			// is embedded in driverState, address stays fixed, one-time set.
			d.userData.AtofCtx = unsafe.Pointer(&d.atofCtx[0])
			return d
		},
	}
)

// scratch pool retention threshold: scratch capacities above this are
// discarded on release to avoid holding large buffers across requests.
const scratchKeepThreshold = 64 * 1024

// kvBuf pool retention threshold.
const kvBufKeepThreshold = 32 * 1024

// mapKVBufCount is the KV buffer slot count per MAP frame. C-side hooks
// yield FLUSH_MAP when the buffer fills or end_object closes the map.
//
// Must match C-side NDEC_MAP_KV_BUF_COUNT; mismatches cause the C-side
// BEGIN_MAP fast path to bump sub-region sizes inconsistent with the
// Go-side FLUSH_MAP slot stride, corrupting kv slot base calculations.
const mapKVBufCount = 32

// noscan slab capacity bound. Budget is accumulated from buildStructInfo;
// this clamp prevents extreme types (large elem * large cap) from making
// the slab unmanageable.
const slabCapMax = 64 * 1024

// acquireDriverState gets a driverState from the pool.
func acquireDriverState() *driverState {
	if d := poolFast.Swap(nil); d != nil {
		return d
	}
	return driverPool.Get().(*driverState)
}

// releaseDriverState returns a driverState to the pool.
//
// Slab handling: dispatched hdr.data pointers indirectly hold the slab
// backing array through caller-owned slice headers. The driver drops its
// own reference (d.slab = nil) without waiting for the caller to release
// results.
func releaseDriverState(d *driverState) {
	if cap(d.scratch) > scratchKeepThreshold {
		d.scratch = nil
	} else {
		d.scratch = d.scratch[:0]
	}
	if cap(d.kvBuf) > kvBufKeepThreshold {
		d.kvBuf = nil
	} else {
		d.kvBuf = d.kvBuf[:0]
	}
	d.slab = nil
	if poolFast.CompareAndSwap(nil, d) {
		return
	}
	driverPool.Put(d)
}

func init() {
	// Pre-warm the driver pool.
	poolFast.Store(driverPool.New().(*driverState))
	driverPool.Put(driverPool.New())
}

// reset clears only the state that the next Unmarshal call reads before the
// parser overwrites everything else. The 10 KB frame array is left intact
// because bootstrap logic consults only depth zero, and later pushes rewrite
// each active frame before use.
//
// Depth, PrevInString, PrevEscape, and PendingAction must be reset because the
// parser or driver reads them on entry. StructuralBits, LastBackslash,
// PrevStructuralOrWs, YieldFlags, and ScratchLen are overwritten before any
// subsequent read, so clearing them would only add churn.
func (d *driverState) reset() {
	d.ctx.Depth = 0
	d.ctx.ScanState.PrevInString = 0
	d.ctx.ScanState.PrevEscape = 0
	d.userData.PendingAction = 0
}

// ensureScratchCap pre-allocates scratch for the entire Unmarshal call.
// The upper bound is len(input) because unescape output is always shorter
// than the raw escape sequence (e.g. \uXXXX is at least 6 bytes vs at
// most 4 UTF-8 output bytes).
func (d *driverState) ensureScratchCap(n int) {
	if cap(d.scratch) < n {
		d.scratch = make([]byte, 0, n)
	} else {
		d.scratch = d.scratch[:0]
	}
}

// ensureKvBuf reserves initial capacity for MAP frame KV buffers.
// Budget comes from builtType.mapKVBufBudget.
// A zero budget (no statically visible map fields) resets len without alloc.
// When budget is insufficient, the fast path yields to let Go grow + rebase.
// Capacity is clamped to the static upper bound.
func (d *driverState) ensureKvBuf(budget int) {
	if budget == 0 {
		d.kvBuf = d.kvBuf[:0]
		return
	}
	if cap(d.kvBuf) < budget {
		d.kvBuf = make([]byte, 0, budget)
	} else {
		d.kvBuf = d.kvBuf[:0]
	}
}

// syncKvBufCursor mirrors d.kvBuf's (base, len, cap) into userData so the
// C-side fast path sees the current buffer boundaries. Must be called after
// every Go-side write to d.kvBuf (driver entry pre-fill, reserveMapKVBuf
// grow, shrink).
//
// When cap == 0, base is set to nil so the fast path immediately falls
// through to the yield path (kv_buf_len + need compares to 0).
func (d *driverState) syncKvBufCursor() {
	if cap(d.kvBuf) == 0 {
		d.userData.KvBufBase = nil
		d.userData.KvBufLen = 0
		d.userData.KvBufCap = 0
		return
	}
	full := d.kvBuf[:cap(d.kvBuf)]
	d.userData.KvBufBase = unsafe.Pointer(unsafe.SliceData(full))
	d.userData.KvBufLen = uint32(len(d.kvBuf))
	d.userData.KvBufCap = uint32(cap(d.kvBuf))
}

// ensureSlab pre-allocates the noscan slice backing arena for this
// Unmarshal call. Capacity comes from builtType.noscanSliceBudget.
// A zero budget (no noscan slice fields) skips mallocgc.
//
// Once set, capacity never grows within the call; reallocation would
// invalidate dispatched hdr.data pointers. Underestimated requests fall
// back to individual make([]byte). budget is estimated from initialSliceCap
// and does not cover doubling growth.
func (d *driverState) ensureSlab(budget int) {
	if budget == 0 {
		d.slab = nil
		return
	}
	if budget > slabCapMax {
		budget = slabCapMax
	}
	d.slab = make([]byte, 0, budget)
}

// runUnmarshal decodes input into a known-type dst using the native parser.
func (d *driverState) runUnmarshal(bt *typeInfo, dst unsafe.Pointer, input []byte) error {
	d.reset()
	d.ensureScratchCap(len(input))
	d.ensureSlab(bt.noscanSliceBudget())
	d.ensureKvBuf(bt.mapKVBufBudget())
	d.syncKvBufCursor()

	// Store root type info for error path field name / type resolution.
	d.rootBT = bt

	// Sentinel frame convention (parser state machine symmetry):
	//   frames[0]: owned by the parser. Driver writes phase=ROOT_VALUE +
	//              BindContainerKind=INVALID so begin_object/end_object/etc
	//              hooks default-fall-through to root_done on the parent
	//              path. Bootstrap pushes depth from 0 to 1; this frame
	//              exists from then on. BindType/BindDst are not read.
	//   frames[1]: root binding's child slot, pre-filled by the driver.
	//              root_value sees '{'/'[' then STACK_PUSH to depth==2;
	//              begin_object/begin_array hook sees child = frames[1].
	//              Root scalars do not STACK_PUSH; at depth==1 the root
	//              scalar hook reads frames[1]'s binding directly.
	sentinel := &d.ctx.Frames[0]
	sentinel.Phase = uint32(phaseRootValue)
	sentinel.Data = 0
	sentinel.BindContainerKind = uint8(bkInvalid)
	sentinel.BindPendingFieldIdx = -1

	root := &d.ctx.Frames[1]
	root.BindType = unsafe.Pointer(&bt.base)
	root.BindDst = dst
	root.BindContainerKind = uint8(bt.kind())
	root.BindPendingFieldIdx = -1

	if cap(d.scratch) > 0 {
		d.userData.ScratchPtr = unsafe.Pointer(unsafe.SliceData(d.scratch[:cap(d.scratch)]))
		d.userData.ScratchCap = uint32(cap(d.scratch))
	} else {
		d.userData.ScratchPtr = nil
		d.userData.ScratchCap = 0
	}
	d.userData.ScratchLen = 0

	var bufHead unsafe.Pointer
	var bufBase uintptr
	var bufEnd uintptr
	if len(input) > 0 {
		bufHead = unsafe.Pointer(unsafe.SliceData(input))
		bufBase = uintptr(bufHead)
		bufEnd = bufBase + uintptr(len(input))
	}

	d.ctx.Buf = bufHead
	d.ctx.BufEnd = bufEnd
	d.ctx.CurPos = bufBase
	d.ctx.ChunkPtr = bufBase
	d.ctx.UserData = unsafe.Pointer(&d.userData)
	d.ctx.ScanState.PrevStructuralOrWs = 1
	d.ctx.ScanState.IsFinal = 1 // P1 single-shot: input is fully available

	// userData.BufEnd mirrors ctx.BufEnd so number scalar hooks can
	// read the input end without chasing through frames to find ctx.
	// Used by the atof padded path to check whether NDEC_ATOF_PADDED_TAIL
	// bytes remain readable after the token. Stored as uintptr because
	// input+len is a one-past-end sentinel.
	d.userData.BufEnd = bufEnd

	for {
		nativendec.ParseDefault(unsafe.Pointer(&d.ctx))

		switch d.ctx.ExitCode {
		case exitOK:
			runtime.KeepAlive(input)
			runtime.KeepAlive(d)
			return nil

		case exitSuspend:
			if d.userData.PendingAction != uint32(yaNone) {
				if err := d.dispatchYield(); err != nil {
					runtime.KeepAlive(input)
					runtime.KeepAlive(d)
					return err
				}
				d.userData.PendingAction = uint32(yaNone)
				continue
			}
			runtime.KeepAlive(input)
			return errUnexpectedEOF

		default:
			err := translateNdecError(d.ctx.ExitCode, d.ctx.ErrorPos)
			runtime.KeepAlive(input)
			return err
		}
	}
}
