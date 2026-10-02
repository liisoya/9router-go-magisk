package shutdown

import (
	"sync/atomic"
	"testing"
	"time"
)

func TestCancelClosesDone(t *testing.T) {
	TestReset()
	defer TestReset()
	if Fired() {
		t.Fatal("should not be fired initially")
	}
	Cancel()
	Cancel() // idempotent — must not panic on double close
	if !Fired() {
		t.Fatal("should be fired after Cancel")
	}
	select {
	case <-Done():
	default:
		t.Fatal("Done() should be closed after Cancel")
	}
}

func TestContextCanceledOnCancel(t *testing.T) {
	TestReset()
	defer TestReset()
	ctx := Context()
	select {
	case <-ctx.Done():
		t.Fatal("context must not be done before Cancel")
	default:
	}
	Cancel()
	select {
	case <-ctx.Done():
	default:
		t.Fatal("context must be done after Cancel")
	}
}

func TestRequestStopClosesBothChannels(t *testing.T) {
	TestReset()
	defer TestReset()

	select {
	case <-StopRequested():
		t.Fatal("StopRequested must be open before RequestStop")
	default:
	}

	RequestStop()
	RequestStop() // idempotent — a second request must not panic

	select {
	case <-StopRequested():
	default:
		t.Error("StopRequested must be closed after RequestStop")
	}
	select {
	case <-Done():
	default:
		t.Error("RequestStop must also cancel in-flight work")
	}
	if !Fired() {
		t.Error("RequestStop must fire shutdown so the Fx OnStop hook drains")
	}
}

// Cancel must NOT close StopRequested: a ^C is not an operator request for the
// process to exit, and main must keep waiting for a real stop after a stray
// cancellation from a stream.
func TestCancelLeavesStopRequestOpen(t *testing.T) {
	TestReset()
	defer TestReset()

	Cancel()
	select {
	case <-StopRequested():
		t.Fatal("Cancel must not close StopRequested")
	default:
	}
}

func TestRunAfterStopRunsHookOnce(t *testing.T) {
	TestReset()
	defer TestReset()

	var calls atomic.Int32
	RestartAfterStop(func() { calls.Add(1) })

	select {
	case <-StopRequested():
	default:
		t.Fatal("RestartAfterStop must request the stop")
	}

	RunAfterStop()
	RunAfterStop() // must not re-run the replacement spawn
	if got := calls.Load(); got != 1 {
		t.Fatalf("restart hook ran %d times, want 1", got)
	}
}

// The hook must run after the listener is drained, so it has to survive a stop
// request that arrives first: main registers, then runs, and the hook must
// still be pending when main reaches RunAfterStop.
func TestAfterStopHookRegisteredBeforeRequest(t *testing.T) {
	TestReset()
	defer TestReset()

	ran := make(chan struct{})
	RestartAfterStop(func() { close(ran) })

	deadline := time.After(time.Second)
	select {
	case <-ran:
		t.Fatal("hook must not run until RunAfterStop")
	case <-deadline:
	}

	RunAfterStop()
	select {
	case <-ran:
	default:
		t.Fatal("hook must run once RunAfterStop is called")
	}
}
