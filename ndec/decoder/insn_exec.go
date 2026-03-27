// insn_exec materializes interface{} values from native-side instructions.
package decoder

import (
	"encoding/binary"
	"math"
	"strconv"
	"unsafe"

	"github.com/velox-io/json/ndec"
)

// Must stay in sync with native instruction tags.
const (
	tagSetTarget   = 0x01
	tagSetKey      = 0x02
	tagMakeObject  = 0x10
	tagMakeArray   = 0x11
	tagCloseObject = 0x12
	tagCloseArray  = 0x13
	tagEmitNull    = 0x20
	tagEmitTrue    = 0x21
	tagEmitFalse   = 0x22
	tagEmitInt     = 0x23
	tagEmitString  = 0x26
	tagEmitStrEsc  = 0x27
	tagEmitNumber  = 0x28
)

type containerFrame struct {
	isArray bool
	m       map[string]any
	a       []any
	key     string
}

type insnExecState struct {
	target unsafe.Pointer
	stack  []containerFrame
}

func (s *insnExecState) reset() {
	s.target = nil
	s.stack = s.stack[:0]
}

func (st *decState) executeInsn(ctx *ndec.DecExecCtx) {
	buf := st.insn[:ctx.InsnLen]
	s := &st.insnExec
	scratchBase := unsafe.Pointer(&st.scratch[0])
	i := 0

	for i < len(buf) {
		tag := buf[i]
		i++

		switch tag {
		case tagSetTarget:
			ptr := binary.LittleEndian.Uint64(buf[i:])
			i += 8
			s.target = *(*unsafe.Pointer)(unsafe.Pointer(&ptr))

		case tagSetKey:
			kp := binary.LittleEndian.Uint64(buf[i:])
			kl := binary.LittleEndian.Uint32(buf[i+8:])
			i += 12
			var key string
			sh := (*[2]uintptr)(unsafe.Pointer(&key))
			sh[0] = uintptr(*(*unsafe.Pointer)(unsafe.Pointer(&kp)))
			sh[1] = uintptr(kl)
			if len(s.stack) > 0 {
				s.stack[len(s.stack)-1].key = key
			}

		case tagMakeObject:
			i += 4
			s.stack = append(s.stack, containerFrame{
				isArray: false,
				m:       make(map[string]any),
			})

		case tagMakeArray:
			i += 4
			s.stack = append(s.stack, containerFrame{
				isArray: true,
				a:       make([]any, 0, 4),
			})

		case tagCloseObject, tagCloseArray:
			n := len(s.stack)
			if n == 0 {
				continue
			}
			top := s.stack[n-1]
			s.stack = s.stack[:n-1]
			var val any
			if top.isArray {
				val = top.a
			} else {
				val = top.m
			}
			s.emitValue(val)

		case tagEmitNull:
			s.emitValue(nil)

		case tagEmitTrue:
			s.emitValue(true)

		case tagEmitFalse:
			s.emitValue(false)

		case tagEmitInt:
			v := int64(binary.LittleEndian.Uint64(buf[i:]))
			i += 8
			s.emitValue(float64(v))

		case tagEmitString:
			sp := binary.LittleEndian.Uint64(buf[i:])
			sl := binary.LittleEndian.Uint32(buf[i+8:])
			i += 12
			var str string
			sh := (*[2]uintptr)(unsafe.Pointer(&str))
			sh[0] = uintptr(*(*unsafe.Pointer)(unsafe.Pointer(&sp)))
			sh[1] = uintptr(sl)
			s.emitValue(str)

		case tagEmitStrEsc:
			off := binary.LittleEndian.Uint32(buf[i:])
			sl := binary.LittleEndian.Uint32(buf[i+4:])
			i += 8
			arenaPtr := unsafe.Add(scratchBase, uintptr(off))
			var str string
			sh := (*[2]uintptr)(unsafe.Pointer(&str))
			sh[0] = uintptr(arenaPtr)
			sh[1] = uintptr(sl)
			s.emitValue(str)

		case tagEmitNumber:
			np := binary.LittleEndian.Uint64(buf[i:])
			nl := binary.LittleEndian.Uint32(buf[i+8:])
			i += 12
			rawPtr := *(*unsafe.Pointer)(unsafe.Pointer(&np))
			raw := unsafe.Slice((*byte)(rawPtr), int(nl))
			f, err := strconv.ParseFloat(unsafeString(raw), 64)
			if err != nil {
				f = math.NaN()
			}
			s.emitValue(f)

		default:
			return
		}
	}
}

func (s *insnExecState) emitValue(val any) {
	n := len(s.stack)
	if n > 0 {
		top := &s.stack[n-1]
		if top.isArray {
			top.a = append(top.a, val)
		} else {
			top.m[top.key] = val
		}
		return
	}
	if s.target != nil {
		*(*any)(s.target) = val
		s.target = nil
	}
}

func unsafeString(b []byte) string {
	return *(*string)(unsafe.Pointer(&b))
}
