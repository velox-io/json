package bind

import (
	"io"
	"runtime"
	"sync"
	"testing"
)

// GC stress for the feed doc lifecycle on the Decoder path, mirroring
// TestStreamScopeRotationHoldGC for scopeview documents. The in-flight
// value's document is rooted through the driver state, and a published
// document lives on through the destination's Value descriptors; the
// machine's ValueDoc field is noscan and is not a GC root, so both roots
// must hold while a concurrent mark runs.
//
// A 4 KiB window forces arena growth on nearly every value, and every 64th
// element is retained, so the held documents are re-read after later values
// regrew and relocated the arenas under them. Both cadences sample generator
// indices of the form 64k-1, never its every-8th null Values.

func TestDecoderDocRetentionGC(t *testing.T) {
	stop := make(chan struct{})
	var spinner sync.WaitGroup
	spinner.Add(1)
	go func() {
		defer spinner.Done()
		for {
			select {
			case <-stop:
				return
			default:
				runtime.GC()
			}
		}
	}()
	defer func() {
		close(stop)
		spinner.Wait()
	}()

	src := newNdjsonReader(32 << 20)
	d := NewDecoder(src, WithBufferSize(1<<12))

	var count int64
	var held []stressElem
	var heldIdx []int64
	for {
		var e stressElem
		err := d.Decode(&e)
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatalf("Decode at value %d: %v", count, err)
		}
		count++
		if count%256 == 0 {
			if err := checkValueRead(count-1, &e); err != nil {
				t.Fatalf("live %d: %v", count-1, err)
			}
		}
		if count%64 == 0 {
			held = append(held, e)
			heldIdx = append(heldIdx, count-1)
		}
	}
	for i := range held {
		if err := checkValueRead(heldIdx[i], &held[i]); err != nil {
			t.Fatalf("held %d (item %d): %v", i, heldIdx[i], err)
		}
	}
	if count == 0 {
		t.Fatal("decoded no values")
	}
	t.Logf("values=%d held=%d", count, len(held))
}
