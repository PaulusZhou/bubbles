package feishu

import (
	"context"
	"errors"
	"sync"
	"time"
)

// cardOpTimeout bounds card flushes and failure notices that must survive a
// canceled run context.
const cardOpTimeout = 30 * time.Second

// Run abort causes, attached to the run context via context.WithCancelCause so
// callers can tell a user stop from a watchdog abort after the fact.
var (
	errRunStoppedByUser = errors.New("stopped by user")
	errRunStalled       = errors.New("长时间无进展")
	errRunExceeded      = errors.New("超过最长执行时间")
)

// Defaults for chat run bounding. A fixed wall-clock deadline is wrong for
// long multi-step agent tasks: what matters is whether the run is still making
// progress, not how long it has been alive.
const (
	defaultRunStall = 30 * time.Minute
	defaultRunMax   = 4 * time.Hour
)

// runWatchdog bounds an agent run by *inactivity* instead of a fixed deadline:
// every stream event re-arms the stall timer via Touch, so an actively
// progressing run is never cut off. A hung run (no events for stall) or a
// runaway run (total duration over max) is aborted by canceling the context
// with a descriptive cause.
type runWatchdog struct {
	cancel context.CancelCauseFunc
	stall  time.Duration

	mu         sync.Mutex
	stallTimer *time.Timer
	maxTimer   *time.Timer
	closed     bool
}

// newRunContext derives a run context from parent governed by a watchdog.
// Callers must Touch on every stream event and Close when the run is done.
// Non-positive stall/max fall back to the defaults.
func newRunContext(parent context.Context, stall, max time.Duration) (context.Context, *runWatchdog) {
	if stall <= 0 {
		stall = defaultRunStall
	}
	if max <= 0 {
		max = defaultRunMax
	}
	ctx, cancel := context.WithCancelCause(parent)
	wd := &runWatchdog{cancel: cancel, stall: stall}
	wd.stallTimer = time.AfterFunc(stall, func() { cancel(errRunStalled) })
	wd.maxTimer = time.AfterFunc(max, func() { cancel(errRunExceeded) })
	return ctx, wd
}

// Touch re-arms the stall timer. Call it whenever the run makes progress.
func (w *runWatchdog) Touch() {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.closed {
		return
	}
	w.stallTimer.Reset(w.stall)
}

// Close stops the watchdog timers. Safe to call multiple times.
func (w *runWatchdog) Close() {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.closed {
		return
	}
	w.closed = true
	w.stallTimer.Stop()
	w.maxTimer.Stop()
}
