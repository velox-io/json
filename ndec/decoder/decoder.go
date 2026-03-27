package decoder

import (
	"io"
	"reflect"
	"strconv"
	"sync"
	"unsafe"

	"github.com/velox-io/json/gort"
	"github.com/velox-io/json/jerr"
	"github.com/velox-io/json/ndec"
	"github.com/velox-io/json/vdec"
)

const (
	defaultChunkSize = 4096
	arenaSize        = 64 * 1024
	maxResumeFrames  = 128
	resumeFrameSize  = 32 // must match native ResumeFrame / GsdecFrame size
	insnSize         = 4096
)

// Decoder is a lightweight handle over pooled native decode state.
type Decoder struct {
	drv       *ndec.Driver
	r         io.Reader
	chunkSize int
	err       error // sticky error
}

// decState owns the pooled buffers used by one Decode call.
type decState struct {
	drv       *ndec.Driver
	r         io.Reader
	chunkSize int

	buf    []byte
	bufLen int
	srcOff int
	eof    bool

	resumeStack []byte
	scratch     []byte
	insn        []byte
	insnExec    insnExecState

	pendingSlices [maxResumeFrames]*gort.SliceHeader
	pendingDepth  int

	mapState *mapDecState
	oldBufs  [][]byte
}

var statePool = sync.Pool{
	New: func() any {
		return &decState{
			resumeStack: make([]byte, maxResumeFrames*resumeFrameSize),
			scratch:     make([]byte, arenaSize),
			insn:        make([]byte, insnSize),
		}
	},
}

// prepare resets transient state and reuses existing buffers when possible.
func (s *decState) prepare(drv *ndec.Driver, r io.Reader, chunkSize int) {
	s.drv = drv
	s.r = r
	s.chunkSize = chunkSize
	s.bufLen = 0
	s.srcOff = 0
	s.eof = false
	s.pendingDepth = 0
	s.mapState = nil
	s.oldBufs = s.oldBufs[:0]
	s.insnExec.reset()
	if cap(s.buf) < chunkSize {
		s.buf = nil
	}
}

// release drops references that should not escape back into the pool.
func (s *decState) release() {
	s.drv = nil
	s.r = nil
	s.mapState = nil
	s.oldBufs = s.oldBufs[:0]
}

// New creates a Decoder with the default chunk size.
func New(r io.Reader, drv *ndec.Driver) *Decoder {
	return NewWithChunkSize(r, drv, defaultChunkSize)
}

// NewWithChunkSize creates a Decoder with an explicit chunk size.
func NewWithChunkSize(r io.Reader, drv *ndec.Driver, chunkSize int) *Decoder {
	if chunkSize < 16 {
		chunkSize = 16
	}
	return &Decoder{
		drv:       drv,
		r:         r,
		chunkSize: chunkSize,
	}
}

// Decode reads the next value into v.
func (d *Decoder) Decode(v any) error {
	if d.err != nil {
		return d.err
	}
	if !d.drv.Available {
		d.err = &jerr.SyntaxError{Offset: 0}
		return d.err
	}

	rv := reflect.ValueOf(v)
	if rv.Kind() != reflect.Pointer || rv.IsNil() {
		return &jerr.SyntaxError{Offset: 0}
	}
	elemType := rv.Elem().Type()

	ti := getTypeInfo(elemType)
	descRoot := getOrCompileDecTypeDesc(ti)
	if descRoot == nil {
		return &jerr.SyntaxError{Offset: 0}
	}

	st := statePool.Get().(*decState)
	st.prepare(d.drv, d.r, d.chunkSize)

	err := st.decodeValue(rv, descRoot)

	st.release()
	statePool.Put(st)

	if err != nil {
		d.err = err
	}
	return err
}

func (s *decState) decodeValue(rv reflect.Value, descRoot unsafe.Pointer) error {
	ptr := rv.UnsafePointer()

	if err := s.initialFill(); err != nil {
		return err
	}

	var ctx ndec.DecExecCtx
	ctx.SrcPtr = uintptr(unsafe.Pointer(&s.buf[0]))
	ctx.SrcLen = uint32(s.bufLen)
	ctx.CurBase = ptr
	ctx.TiPtr = uintptr(descRoot)
	ctx.ScratchPtr = uintptr(unsafe.Pointer(&s.scratch[0]))
	ctx.ScratchCap = uint32(len(s.scratch))
	ctx.ResumePtr = uintptr(unsafe.Pointer(&s.resumeStack[0]))
	ctx.ResumeCap = maxResumeFrames
	ctx.InsnPtr = uintptr(unsafe.Pointer(&s.insn[0]))
	ctx.InsnCap = uint32(len(s.insn))
	ctx.InsnLen = 0

	s.pendingDepth = 0
	s.insnExec.reset()
	s.oldBufs = s.oldBufs[:0]

	s.drv.Exec(unsafe.Pointer(&ctx))

	for {
		switch ctx.ExitCode {
		case ndec.ExitOK:
			if ctx.InsnLen > 0 {
				s.executeInsn(&ctx)
			}
			return nil

		case ndec.ExitUnexpectedEOF:
			if ctx.InsnLen > 0 {
				s.executeInsn(&ctx)
				ctx.InsnLen = 0
			}
			if s.eof {
				return io.ErrUnexpectedEOF
			}
			checkpoint := int(ctx.Idx)
			if err := s.refillNewBuffer(checkpoint); err != nil {
				return err
			}
			ctx.SrcPtr = uintptr(unsafe.Pointer(&s.buf[0]))
			ctx.SrcLen = uint32(s.bufLen)
			ctx.Idx = 0
			s.drv.Resume(unsafe.Pointer(&ctx))

		case ndec.YieldArenaFull:
			needed := int(ctx.YieldParam0)
			newSize := max(len(s.scratch)*2, needed+1024)
			s.scratch = make([]byte, newSize)
			ctx.ScratchPtr = uintptr(unsafe.Pointer(&s.scratch[0]))
			ctx.ScratchCap = uint32(newSize)
			ctx.ScratchLen = 0
			s.drv.Resume(unsafe.Pointer(&ctx))

		case ndec.YieldAllocSlice:
			sh := s.handleYieldAllocSlice(&ctx)
			if s.pendingDepth < maxResumeFrames {
				s.pendingSlices[s.pendingDepth] = sh
				s.pendingDepth++
			}
			s.drv.Resume(unsafe.Pointer(&ctx))

		case ndec.YieldGrowSlice:
			s.handleYieldGrowSlice(&ctx)
			s.drv.Resume(unsafe.Pointer(&ctx))

		case ndec.YieldMapInit:
			s.handleYieldMapInit(&ctx)
			s.drv.Resume(unsafe.Pointer(&ctx))

		case ndec.YieldMapAssign:
			if ctx.InsnLen > 0 {
				s.executeInsn(&ctx)
				ctx.InsnLen = 0
			}
			s.handleYieldMapAssign(&ctx)
			s.drv.Resume(unsafe.Pointer(&ctx))

		case ndec.YieldInsnFlush:
			s.executeInsn(&ctx)
			ctx.InsnLen = 0
			s.drv.Resume(unsafe.Pointer(&ctx))

		case ndec.YieldAllocPointer:
			s.handleYieldAllocPointer(&ctx)
			s.drv.Resume(unsafe.Pointer(&ctx))

		case ndec.YieldFloatParse:
			s.handleYieldFloatParse(&ctx)
			s.drv.Resume(unsafe.Pointer(&ctx))

		case ndec.ExitSyntaxError:
			return &jerr.SyntaxError{Offset: int64(s.srcOff) + int64(ctx.ErrDetail)}

		case ndec.ExitTypeError:
			return &jerr.SyntaxError{Offset: int64(s.srcOff) + int64(ctx.ErrDetail)}

		default:
			return &jerr.SyntaxError{Offset: int64(s.srcOff) + int64(ctx.Idx)}
		}
	}
}

// ---- Buffer management ----

func (s *decState) initialFill() error {
	if s.bufLen > 0 {
		return nil
	}
	if s.eof {
		return io.EOF
	}
	if cap(s.buf) >= s.chunkSize {
		s.buf = s.buf[:s.chunkSize]
	} else {
		s.buf = make([]byte, s.chunkSize)
	}
	for !s.eof {
		n, err := s.r.Read(s.buf[:s.chunkSize])
		s.bufLen += n
		if err == io.EOF {
			s.eof = true
		} else if err != nil {
			return err
		}
		if s.bufLen > 0 {
			return nil
		}
	}
	return io.EOF
}

func (s *decState) refillNewBuffer(checkpoint int) error {
	oldBuf := s.buf
	oldLen := s.bufLen
	tail := oldLen - checkpoint

	s.srcOff += checkpoint

	newSize := tail + s.chunkSize
	newBuf := make([]byte, newSize)
	if tail > 0 {
		copy(newBuf[:tail], oldBuf[checkpoint:oldLen])
	}

	s.oldBufs = append(s.oldBufs, oldBuf)
	s.buf = newBuf
	s.bufLen = tail

	for s.bufLen == tail && !s.eof {
		n, err := s.r.Read(s.buf[s.bufLen:])
		s.bufLen += n
		if err == io.EOF {
			s.eof = true
		} else if err != nil {
			return err
		}
	}

	if s.bufLen == 0 {
		return io.ErrUnexpectedEOF
	}
	if s.bufLen == tail && s.eof {
		return io.ErrUnexpectedEOF
	}
	return nil
}

// ---- Yield handlers ----

func (s *decState) handleYieldAllocSlice(ctx *ndec.DecExecCtx) *gort.SliceHeader {
	targetPtr := yieldPtr(ctx.YieldParam1)
	elemRType := yieldPtr(ctx.YieldParam2)

	capHint := 4
	backing := gort.UnsafeNewArray(elemRType, capHint)
	writeSliceHeader(targetPtr, backing, capHint, capHint)
	sh := (*gort.SliceHeader)(targetPtr)

	ctx.CurBase = backing
	ctx.YieldParam0 = 0
	ctx.YieldParam1 = uint64(capHint)
	return sh
}

func (s *decState) handleYieldGrowSlice(ctx *ndec.DecExecCtx) {
	count := int(ctx.YieldParam0)
	targetPtr := yieldPtr(ctx.YieldParam1)
	elemRType := yieldPtr(ctx.YieldParam2)

	sh := (*gort.SliceHeader)(targetPtr)
	oldCap := sh.Cap
	newCap := max(oldCap*2, 8)

	newBacking := gort.UnsafeNewArray(elemRType, newCap)
	oldBacking := sh.Data
	if count > 0 {
		gort.TypedSliceCopy(elemRType, newBacking, newCap, oldBacking, count)
	}

	writeSliceHeader(targetPtr, newBacking, newCap, newCap)
	ctx.CurBase = newBacking
	ctx.YieldParam0 = uint64(count)
	ctx.YieldParam1 = uint64(newCap)
}

type mapDecState struct {
	mi         *vdec.DecMapInfo
	mapTypePtr unsafe.Pointer
	mp         unsafe.Pointer
	entryBuf   []byte
	bufCap     int
	stride     int
}

func (s *decState) handleYieldMapInit(ctx *ndec.DecExecCtx) {
	ti := (*vdec.DecTypeInfo)(yieldPtr(ctx.YieldParam0))
	targetPtr := yieldPtr(ctx.YieldParam1)
	empty := ctx.YieldParam2 != 0

	mapTypePtr := ti.TypePtr
	mi := ti.ResolveMap()
	mp := gort.MakeMap(mapTypePtr, 0, nil)
	gort.TypedMemmove(mapTypePtr, targetPtr, unsafe.Pointer(&mp))

	if empty {
		ctx.YieldParam0 = 0
		ctx.YieldParam1 = 0
		ctx.YieldParam2 = 0
		return
	}

	valSize := int(mi.ValSize)
	stride := 16 + valSize
	bufCap := 32
	entryBuf := make([]byte, bufCap*stride)

	ctx.CurBase = unsafe.Pointer(&entryBuf[0])
	ctx.YieldParam0 = 0
	ctx.YieldParam1 = uint64(bufCap)

	s.mapState = &mapDecState{
		mi:         mi,
		mapTypePtr: mapTypePtr,
		mp:         mp,
		entryBuf:   entryBuf,
		bufCap:     bufCap,
		stride:     stride,
	}
}

func (s *decState) handleYieldMapAssign(ctx *ndec.DecExecCtx) {
	count := int(ctx.YieldParam0)
	done := ctx.YieldParam2 != 0
	ms := s.mapState

	valRType := ms.mi.ValRType
	valHasPtr := ms.mi.ValHasPtr
	valSize := ms.mi.ValSize

	for i := range count {
		entryPtr := unsafe.Pointer(&ms.entryBuf[i*ms.stride])
		keyPtr := *(*unsafe.Pointer)(entryPtr)
		keyLen := *(*int)(unsafe.Add(entryPtr, 8))
		valPtr := unsafe.Add(entryPtr, 16)

		var key string
		sh := (*[2]uintptr)(unsafe.Pointer(&key))
		sh[0] = uintptr(keyPtr)
		sh[1] = uintptr(keyLen)

		slot := gort.MapAssignFastStr(ms.mapTypePtr, ms.mp, key)
		if valHasPtr {
			gort.TypedMemmove(valRType, slot, valPtr)
		} else {
			memmove(slot, valPtr, valSize)
		}
	}

	if done {
		s.mapState = nil
		return
	}
	ctx.CurBase = unsafe.Pointer(&ms.entryBuf[0])
}

func (s *decState) handleYieldAllocPointer(ctx *ndec.DecExecCtx) {
	elemRType := yieldPtr(ctx.YieldParam0)
	targetPtr := yieldPtr(ctx.YieldParam1)
	elemPtr := gort.UnsafeNew(elemRType)
	*(*unsafe.Pointer)(targetPtr) = elemPtr
}

func (s *decState) handleYieldFloatParse(ctx *ndec.DecExecCtx) {
	rawPtr := yieldPtr(ctx.YieldParam0)
	rawLen := int(ctx.YieldParam1 & 0xFFFFFFFF)
	kind := uint8(ctx.YieldParam1 >> 32)
	targetPtr := yieldPtr(ctx.YieldParam2)

	raw := unsafe.Slice((*byte)(rawPtr), rawLen)
	f, _ := strconv.ParseFloat(unsafeString(raw), 64)

	if kind == 12 { // KIND_FLOAT32
		*(*float32)(targetPtr) = float32(f)
	} else {
		*(*float64)(targetPtr) = f
	}
}

//go:nosplit
func yieldPtr(v uint64) unsafe.Pointer {
	return *(*unsafe.Pointer)(unsafe.Pointer(&v))
}
