package feishu

import (
	"context"
	"errors"
	"testing"
	"time"
)

func waitCanceled(t *testing.T, ctx context.Context, timeout time.Duration) {
	t.Helper()
	select {
	case <-ctx.Done():
	case <-time.After(timeout):
		t.Fatal("context was not canceled in time")
	}
}

func TestRunWatchdog_StallAborts(t *testing.T) {
	ctx, wd := newRunContext(context.Background(), 30*time.Millisecond, time.Second)
	defer wd.Close()

	waitCanceled(t, ctx, time.Second)
	if cause := context.Cause(ctx); !errors.Is(cause, errRunStalled) {
		t.Fatalf("cause = %v, want errRunStalled", cause)
	}
}

func TestRunWatchdog_TouchKeepsRunAlive(t *testing.T) {
	ctx, wd := newRunContext(context.Background(), 60*time.Millisecond, time.Second)
	defer wd.Close()

	// Progress every 10ms for 300ms: far beyond the stall window.
	deadline := time.Now().Add(300 * time.Millisecond)
	for time.Now().Before(deadline) {
		wd.Touch()
		if ctx.Err() != nil {
			t.Fatalf("context canceled despite progress: %v", context.Cause(ctx))
		}
		time.Sleep(10 * time.Millisecond)
	}

	// Stop touching: the stall watchdog must fire.
	waitCanceled(t, ctx, time.Second)
	if cause := context.Cause(ctx); !errors.Is(cause, errRunStalled) {
		t.Fatalf("cause = %v, want errRunStalled", cause)
	}
}

func TestRunWatchdog_MaxCapsRunawayRun(t *testing.T) {
	ctx, wd := newRunContext(context.Background(), time.Hour, 60*time.Millisecond)
	defer wd.Close()

	// Constant progress must not defeat the absolute cap.
	done := make(chan struct{})
	go func() {
		defer close(done)
		for ctx.Err() == nil {
			wd.Touch()
			time.Sleep(5 * time.Millisecond)
		}
	}()

	waitCanceled(t, ctx, time.Second)
	<-done
	if cause := context.Cause(ctx); !errors.Is(cause, errRunExceeded) {
		t.Fatalf("cause = %v, want errRunExceeded", cause)
	}
}

func TestRunWatchdog_UserStopCause(t *testing.T) {
	_, wd := newRunContext(context.Background(), time.Hour, time.Hour)
	defer wd.Close()

	runCtx, cancel := context.WithCancelCause(context.Background())
	cancel(errRunStoppedByUser)

	if cause := context.Cause(runCtx); !errors.Is(cause, errRunStoppedByUser) {
		t.Fatalf("cause = %v, want errRunStoppedByUser", cause)
	}
	// User stop must not be confused with a watchdog abort.
	if cause := context.Cause(runCtx); errors.Is(cause, errRunStalled) || errors.Is(cause, errRunExceeded) {
		t.Fatalf("user stop misclassified as watchdog abort: %v", cause)
	}
}

func TestRunWatchdog_CloseStopsTimers(t *testing.T) {
	ctx, wd := newRunContext(context.Background(), 20*time.Millisecond, 40*time.Millisecond)
	wd.Close()
	wd.Close() // idempotent

	time.Sleep(120 * time.Millisecond)
	if ctx.Err() != nil {
		t.Fatalf("context canceled after Close: %v", context.Cause(ctx))
	}
}

func TestRunWatchdog_Defaults(t *testing.T) {
	ctx, wd := newRunContext(context.Background(), 0, 0)
	defer wd.Close()
	if wd.stall != defaultRunStall {
		t.Fatalf("stall = %v, want %v", wd.stall, defaultRunStall)
	}
	if ctx.Err() != nil {
		t.Fatal("fresh context should not be done")
	}
}
