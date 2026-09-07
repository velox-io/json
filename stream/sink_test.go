package stream_test

import (
	"errors"
	"sync"
	"testing"
	"unsafe"

	"github.com/velox-io/json/stream"
)

// fakeDriver records the element addresses a Sink submits and can be scripted
// to fail on a given call.
type fakeDriver struct {
	calls    []unsafe.Pointer
	failOn   int // 1-based; 0 never fails
	failErr  error
	callsMu  sync.Mutex
	inFlight bool
}

func (d *fakeDriver) EncodeValue(p unsafe.Pointer) error {
	d.callsMu.Lock()
	d.calls = append(d.calls, p)
	if d.inFlight {
		d.callsMu.Unlock()
		panic("fakeDriver: concurrent EncodeValue")
	}
	d.inFlight = true
	d.callsMu.Unlock()
	defer func() { d.inFlight = false }()

	if d.failOn > 0 && len(d.calls) == d.failOn {
		return d.failErr
	}
	return nil
}

func newProducer(n int) func(stream.Sink[int]) error {
	return func(sink stream.Sink[int]) error {
		for i := range n {
			v := i
			if err := sink.Encode(&v); err != nil {
				return err
			}
		}
		return nil
	}
}

func TestOnWriteOverwriteAndClear(t *testing.T) {
	var s stream.Stream[int]

	s.OnWrite(func(stream.Sink[int]) error {
		t.Fatal("first registration must be overwritten")
		return nil
	})
	s.OnWrite(newProducer(2))

	driver := &fakeDriver{}
	configured, err := s.ActivateWrite(driver)
	if err != nil || !configured {
		t.Fatalf("activation: configured=%v err=%v", configured, err)
	}
	if len(driver.calls) != 2 {
		t.Fatalf("second registration must replace the first: %d calls", len(driver.calls))
	}

	s.OnWrite(nil)
	configured, err = s.ActivateWrite(&fakeDriver{})
	if err != nil || configured {
		t.Fatalf("clear: configured=%v err=%v", configured, err)
	}
}

func TestActivateWriteUnconfigured(t *testing.T) {
	var s stream.Stream[int]
	configured, err := s.ActivateWrite(&fakeDriver{})
	if configured || err != nil {
		t.Fatalf("unconfigured stream must report configured=false, got %v/%v", configured, err)
	}
}

func TestSinkEscapedAfterHandler(t *testing.T) {
	var s stream.Stream[int]
	var escaped stream.Sink[int]
	s.OnWrite(func(sink stream.Sink[int]) error {
		escaped = sink
		return nil
	})

	if _, err := s.ActivateWrite(&fakeDriver{}); err != nil {
		t.Fatal(err)
	}
	v := 1
	if err := escaped.Encode(&v); !errors.Is(err, stream.ErrSinkClosed) {
		t.Fatalf("post-handler Encode err=%v, want ErrSinkClosed", err)
	}
}

func TestSinkEncodeNil(t *testing.T) {
	var s stream.Stream[int]
	s.OnWrite(func(sink stream.Sink[int]) error {
		if err := sink.Encode(nil); !errors.Is(err, stream.ErrSinkNilElement) {
			t.Fatalf("Encode(nil) err=%v, want ErrSinkNilElement", err)
		}
		// The state error latches: the next call returns it unchanged.
		v := 1
		if err := sink.Encode(&v); !errors.Is(err, stream.ErrSinkNilElement) {
			t.Fatalf("latched err=%v, want ErrSinkNilElement", err)
		}
		return nil
	})
	if _, err := s.ActivateWrite(&fakeDriver{}); err == nil {
		t.Fatal("activation must fail with the latched state error")
	}
}

func TestSinkReentrant(t *testing.T) {
	var s stream.Stream[int]
	s.OnWrite(func(sink stream.Sink[int]) error {
		v := 1
		_ = sink.Encode(&v)
		return nil
	})
	// Reentrancy is only observable with a driver that calls back; emulate by
	// capturing the sink mid-Encode through the driver.
	var inner stream.Sink[int]
	d := &reentrantDriver{sinkOf: func() stream.Sink[int] { return inner }}
	s.OnWrite(func(sink stream.Sink[int]) error {
		inner = sink
		v := 1
		return sink.Encode(&v)
	})
	if _, err := s.ActivateWrite(d); err != nil {
		t.Fatal(err)
	}
	if d.reentryErr == nil || !errors.Is(d.reentryErr, stream.ErrSinkBusy) {
		t.Fatalf("reentrant Encode err=%v, want ErrSinkBusy", d.reentryErr)
	}
}

type reentrantDriver struct {
	sinkOf     func() stream.Sink[int]
	reentryErr error
}

func (d *reentrantDriver) EncodeValue(p unsafe.Pointer) error {
	v := 2
	d.reentryErr = d.sinkOf().Encode(&v)
	return nil
}

func TestSinkDriverFailsWithErrSinkBusy(t *testing.T) {
	// A driver may fail with the public sentinel itself; the value must
	// latch as a genuine first error rather than read as the busy marker.
	var s stream.Stream[int]
	s.OnWrite(func(sink stream.Sink[int]) error {
		v := 1
		if err := sink.Encode(&v); !errors.Is(err, stream.ErrSinkBusy) {
			t.Fatalf("first Encode err=%v, want ErrSinkBusy", err)
		}
		if err := sink.Encode(&v); !errors.Is(err, stream.ErrSinkBusy) {
			t.Fatalf("latched Encode err=%v, want ErrSinkBusy", err)
		}
		return nil
	})
	d := &fakeDriver{failOn: 1, failErr: stream.ErrSinkBusy}
	if _, err := s.ActivateWrite(d); !errors.Is(err, stream.ErrSinkBusy) {
		t.Fatalf("activation err=%v, want the driver's ErrSinkBusy", err)
	}
	if len(d.calls) != 1 {
		t.Fatalf("post-error calls must be rejected: %d calls", len(d.calls))
	}
}

func TestSinkFirstErrorWins(t *testing.T) {
	sentinel := errors.New("boom")
	var s stream.Stream[int]
	s.OnWrite(func(sink stream.Sink[int]) error {
		v := 1
		if err := sink.Encode(&v); err != nil {
			return err
		}
		// Ignore the encode error and keep producing.
		_ = sink.Encode(&v)
		_ = sink.Encode(&v)
		return nil // returning nil must not mask the latched error
	})

	driver := &fakeDriver{failOn: 2, failErr: sentinel}
	_, err := s.ActivateWrite(driver)
	if !errors.Is(err, sentinel) {
		t.Fatalf("activation err=%v, want the latched first error", err)
	}
	if len(driver.calls) != 2 {
		t.Fatalf("post-error calls must be rejected: %d calls", len(driver.calls))
	}
}

func TestSinkHandlerErrorReported(t *testing.T) {
	sentinel := errors.New("handler")
	var s stream.Stream[int]
	s.OnWrite(func(sink stream.Sink[int]) error {
		v := 1
		if err := sink.Encode(&v); err != nil {
			return err
		}
		return sentinel
	})
	if _, err := s.ActivateWrite(&fakeDriver{}); !errors.Is(err, sentinel) {
		t.Fatalf("activation err=%v, want handler error", err)
	}
}

func TestSinkEncodeErrorPrecedesHandlerError(t *testing.T) {
	encErr := errors.New("encode")
	hdlErr := errors.New("handler")
	var s stream.Stream[int]
	s.OnWrite(func(sink stream.Sink[int]) error {
		v := 1
		_ = sink.Encode(&v) // fails
		return hdlErr
	})
	if _, err := s.ActivateWrite(&fakeDriver{failOn: 1, failErr: encErr}); !errors.Is(err, encErr) {
		t.Fatalf("activation err=%v, want the earlier encode error", err)
	}
}

func TestOnWriteIndependentFromOnRead(t *testing.T) {
	var s stream.Stream[int]
	s.OnRead(func(stream.Scope[int]) error { return nil })
	// OnRead alone must not configure the write side.
	configured, err := s.ActivateWrite(&fakeDriver{})
	if configured || err != nil {
		t.Fatalf("OnRead must not configure write side: %v/%v", configured, err)
	}
	s.OnWrite(newProducer(1))
	configured, err = s.ActivateWrite(&fakeDriver{})
	if !configured || err != nil {
		t.Fatalf("OnWrite after OnRead: %v/%v", configured, err)
	}
}

func TestSinkPanicClosesSink(t *testing.T) {
	var s stream.Stream[int]
	var escaped stream.Sink[int]
	s.OnWrite(func(sink stream.Sink[int]) error {
		escaped = sink
		panic("producer")
	})
	func() {
		defer func() {
			if r := recover(); r == nil {
				t.Fatal("panic must propagate")
			}
		}()
		_, _ = s.ActivateWrite(&fakeDriver{})
	}()
	v := 1
	if err := escaped.Encode(&v); !errors.Is(err, stream.ErrSinkClosed) {
		t.Fatalf("post-panic Encode err=%v, want ErrSinkClosed", err)
	}
}

func TestConcurrentStreamEncoders(t *testing.T) {
	// The same configured Stream may be activated by different encoders
	// concurrently; the handler and captured state must be self-safe. A
	// counter-based producer with a mutex exercises the supported pattern.
	var s stream.Stream[int]
	var mu sync.Mutex
	produced := 0
	s.OnWrite(func(sink stream.Sink[int]) error {
		for range 3 {
			mu.Lock()
			produced++
			v := produced
			mu.Unlock()
			if err := sink.Encode(&v); err != nil {
				return err
			}
		}
		return nil
	})

	var wg sync.WaitGroup
	for range 4 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			d := &fakeDriver{}
			if _, err := s.ActivateWrite(d); err != nil {
				t.Error(err)
			}
			if len(d.calls) != 3 {
				t.Errorf("3 elements per activation, got %d", len(d.calls))
			}
		}()
	}
	wg.Wait()
	if produced != 12 {
		t.Fatalf("produced=%d, want 12", produced)
	}
}
