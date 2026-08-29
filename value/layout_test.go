package value

import (
	"testing"
	"unsafe"

	"github.com/velox-io/json/internal/valueabi"
)

const (
	descriptorDocOff  = 0
	descriptorBaseOff = 8
	descriptorTidxOff = 12
	descriptorEndOff  = 16
	descriptorModeOff = 20
	descriptorSize    = 24
	docSize           = 80
)

func TestValueLayoutMatchesDescriptor(t *testing.T) {
	var v Value
	if got := unsafe.Sizeof(v); got != descriptorSize {
		t.Errorf("sizeof Value = %d, want %d", got, descriptorSize)
	}
	if got := unsafe.Offsetof(v.desc); got != 0 {
		t.Errorf("Value.desc offset = %d, want 0", got)
	}
	if got := unsafe.Sizeof(v.desc); got != descriptorSize {
		t.Errorf("sizeof Value.desc = %d, want %d", got, descriptorSize)
	}
}

func TestDescriptorLayoutMatchesNativeABI(t *testing.T) {
	var desc valueabi.Descriptor
	checks := []struct {
		name string
		got  uintptr
		want uintptr
	}{
		{"Doc", unsafe.Offsetof(desc.Doc), descriptorDocOff},
		{"Base", unsafe.Offsetof(desc.Base), descriptorBaseOff},
		{"Tidx", unsafe.Offsetof(desc.Tidx), descriptorTidxOff},
		{"End", unsafe.Offsetof(desc.End), descriptorEndOff},
		{"Mode", unsafe.Offsetof(desc.Mode), descriptorModeOff},
	}
	for _, check := range checks {
		if check.got != check.want {
			t.Errorf("Descriptor.%s offset = %d, want %d", check.name, check.got, check.want)
		}
	}
	if got := unsafe.Sizeof(desc); got != descriptorSize {
		t.Errorf("sizeof Descriptor = %d, want %d", got, descriptorSize)
	}
	if got := unsafe.Offsetof(desc.Mode) + unsafe.Sizeof(desc.Mode); got != unsafe.Sizeof(desc) {
		t.Errorf("Descriptor fields end at %d, size is %d", got, unsafe.Sizeof(desc))
	}
	if got := unsafe.Sizeof(desc.Doc); got != unsafe.Sizeof(uintptr(0)) {
		t.Errorf("sizeof Descriptor.Doc = %d, want %d", got, unsafe.Sizeof(uintptr(0)))
	}
	for name, size := range map[string]uintptr{
		"Base": unsafe.Sizeof(desc.Base),
		"Tidx": unsafe.Sizeof(desc.Tidx),
		"End":  unsafe.Sizeof(desc.End),
		"Mode": unsafe.Sizeof(desc.Mode),
	} {
		if size != 4 {
			t.Errorf("sizeof Descriptor.%s = %d, want 4", name, size)
		}
	}
}

func TestDescriptorTypedLoadStore(t *testing.T) {
	doc := &valueabi.Doc{Tape: make([]uint64, 8)}
	want := valueabi.Descriptor{Doc: doc, Base: 3, Tidx: 1, End: 5, Mode: valueabi.SeamViewB}
	var v Value
	valueabi.Store(unsafe.Pointer(&v), want)
	if v.desc != want {
		t.Errorf("Store produced %+v, want %+v", v.desc, want)
	}
	got := valueabi.Load(unsafe.Pointer(&v))
	if got != want {
		t.Errorf("Load returned %+v, want %+v", got, want)
	}
	got.Tidx++
	if v.desc.Tidx != want.Tidx {
		t.Errorf("Load returned a writable alias: Value tidx = %d, want %d", v.desc.Tidx, want.Tidx)
	}
}

func TestDocLayout(t *testing.T) {
	var doc valueabi.Doc
	checks := []struct {
		name string
		got  uintptr
		want uintptr
	}{
		{"Tape", unsafe.Offsetof(doc.Tape), 0},
		{"StrArena", unsafe.Offsetof(doc.StrArena), 24},
		{"Src", unsafe.Offsetof(doc.Src), 48},
		{"ZeroCopy", unsafe.Offsetof(doc.ZeroCopy), 72},
	}
	for _, check := range checks {
		if check.got != check.want {
			t.Errorf("Doc.%s offset = %d, want %d", check.name, check.got, check.want)
		}
	}
	if got := unsafe.Sizeof(doc); got != docSize {
		t.Errorf("sizeof Doc = %d, want %d", got, docSize)
	}
}
