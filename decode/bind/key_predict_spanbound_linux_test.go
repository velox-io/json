//go:build linux

package bind

import (
	"runtime"
	"syscall"
	"testing"
)

// The Go engine binds the caller's unpadded bytes: it reads no scan padding
// past len(src). A WINDOW-tier lookup whose byte_offset reaches past a short
// member key's closing quote must read the padding byte, 0x20, rather than
// memory past the source. The document ends at the last byte of a page whose
// successor is unmapped, so the overread faults instead of passing silently.

type spanWindowProbe struct {
	A int `json:"shared_long_prefix_alpha"`
	B int `json:"shared_long_prefix_beta"`
}

func TestGoEngineSpanWindowPastSourceEnd(t *testing.T) {
	if !useGoCore() {
		t.Skip("the Go engine does not drive binds in this build")
	}
	// The two keys share their first eighteen bytes, so the tier's
	// byte_offset is seventeen. The final member "y" leaves nine bytes from
	// its body to the end: past the eight keyAt proves readable, below the
	// window the tier reads.
	const doc = `{"shared_long_prefix_beta":1,"y":12345}`
	if len(doc) > syscall.Getpagesize() {
		t.Fatal("doc does not fit a page")
	}
	ps := syscall.Getpagesize()
	mem, err := syscall.Mmap(-1, 0, 2*ps, syscall.PROT_READ|syscall.PROT_WRITE,
		syscall.MAP_PRIVATE|syscall.MAP_ANONYMOUS)
	if err != nil {
		t.Skipf("mmap: %v", err)
	}
	if err := syscall.Mprotect(mem[ps:], syscall.PROT_NONE); err != nil {
		t.Fatalf("mprotect: %v", err)
	}
	defer syscall.Munmap(mem)
	start := ps - len(doc)
	copy(mem[start:ps], doc)
	// cap == len: nothing is readable past the document's last byte.
	src := mem[start:ps:ps]

	var v spanWindowProbe
	if err := Unmarshal(src, &v); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if v != (spanWindowProbe{B: 1}) {
		t.Fatalf("bound %+v, want B=1", v)
	}
	runtime.KeepAlive(mem)
}
