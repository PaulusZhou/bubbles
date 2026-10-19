package feishu

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/larksuite/oapi-sdk-go/v3/channel/types"

	"github.com/pauluszhou/bubbles/internal/agent"
)

// Regression tests for run failure classification and run-context bounding:
// a wall-clock deadline on the message/run context previously fired mid-run
// and surfaced as "❌ 任务执行失败: pi rpc run canceled: context deadline
// exceeded" instead of a timeout notice.

func TestReportRunFailure_DeadlineCauseReportedAsTimeout(t *testing.T) {
	f := &FeishuChannel{ch: &fakeChannel{}}
	ch := f.ch.(*fakeChannel)

	// Expired deadline context, as when an upstream WithTimeout parent fires.
	dctx, cancel := context.WithTimeout(context.Background(), time.Nanosecond)
	defer cancel()
	time.Sleep(2 * time.Millisecond)

	f.reportRunFailure(context.Background(), "oc1", "sk1", dctx,
		fmt.Errorf("pi rpc run canceled: %w", context.DeadlineExceeded))

	texts := ch.sendTexts()
	if len(texts) != 1 {
		t.Fatalf("expected 1 notice, got %d: %v", len(texts), texts)
	}
	if !strings.Contains(texts[0], "任务已中止") || !strings.Contains(texts[0], "超时") {
		t.Errorf("deadline must be reported as a timeout abort, got: %s", texts[0])
	}
	if strings.Contains(texts[0], "任务执行失败") {
		t.Errorf("deadline must not be reported as a task failure, got: %s", texts[0])
	}
}

func TestReportRunFailure_DeadlineErrWithoutCauseReportedAsTimeout(t *testing.T) {
	f := &FeishuChannel{ch: &fakeChannel{}}
	ch := f.ch.(*fakeChannel)

	// Cause is nil (background parent) but the error wraps DeadlineExceeded.
	f.reportRunFailure(context.Background(), "oc1", "sk1", context.Background(),
		fmt.Errorf("pi rpc run canceled: %w", context.DeadlineExceeded))

	texts := ch.sendTexts()
	if len(texts) != 1 || strings.Contains(texts[0], "任务执行失败") {
		t.Fatalf("deadline error must be reported as a timeout abort, got: %v", texts)
	}
}

func TestReportRunFailure_UserStopIsSilent(t *testing.T) {
	f := &FeishuChannel{ch: &fakeChannel{}}
	ch := f.ch.(*fakeChannel)

	ctx, cancel := context.WithCancelCause(context.Background())
	cancel(errRunStoppedByUser)

	f.reportRunFailure(context.Background(), "oc1", "sk1", ctx,
		fmt.Errorf("pi rpc run canceled: %w", context.Canceled))

	if texts := ch.sendTexts(); len(texts) != 0 {
		t.Fatalf("user stop must stay silent, got: %v", texts)
	}
}

func TestReportRunFailure_TransientConnectionErrorIsRetryable(t *testing.T) {
	f := &FeishuChannel{ch: &fakeChannel{}}
	ch := f.ch.(*fakeChannel)

	// A provider blip, as when a run died with "pi run failed: Connection error."
	// after all its tool work had already been persisted.
	f.reportRunFailure(context.Background(), "oc1", "sk1", context.Background(),
		fmt.Errorf("pi run failed: %s", "Connection error."))

	texts := ch.sendTexts()
	if len(texts) != 1 {
		t.Fatalf("expected 1 notice, got %d: %v", len(texts), texts)
	}
	if strings.Contains(texts[0], "任务执行失败") {
		t.Errorf("transient error must not be reported as a task failure, got: %s", texts[0])
	}
	if !strings.Contains(texts[0], "连接中断") || !strings.Contains(texts[0], "重发") {
		t.Errorf("transient error must tell the user how to continue, got: %s", texts[0])
	}
}

func TestIsTransientAgentError(t *testing.T) {
	for _, msg := range []string{
		"pi run failed: Connection error.",
		"send prompt: read tcp: connection reset by peer",
		"API Error: 502 Bad Gateway",
	} {
		if !isTransientAgentError(fmt.Errorf("%s", msg)) {
			t.Errorf("%q should be transient", msg)
		}
	}
	for _, msg := range []string{
		"prompt rejected: tool not found",
		"pi rpc process exited",
		"stream callback error: card too large",
	} {
		if isTransientAgentError(fmt.Errorf("%s", msg)) {
			t.Errorf("%q should not be transient", msg)
		}
	}
	if isTransientAgentError(nil) {
		t.Error("nil error must not be transient")
	}
}

func TestCleanupSessions_KeepsInFlightRun(t *testing.T) {
	f := &FeishuChannel{chatStates: map[string]*chatState{}}
	cs := f.getChatState("oc1")

	stale := time.Now().Add(-2 * sessionExpiry)
	running := &ChatSession{
		key:        "s1",
		lastActive: stale,
		cancelFunc: func(error) {}, // run in flight
	}
	idle := &ChatSession{key: "s2", lastActive: stale}
	cs.sessions["s1"] = running
	cs.sessions["s2"] = idle
	cs.activeKey = "s1"

	f.cleanupSessions()

	if _, ok := cs.sessions["s1"]; !ok {
		t.Error("session with an in-flight run must not be cleaned up")
	}
	if _, ok := cs.sessions["s2"]; ok {
		t.Error("idle expired session should be cleaned up")
	}
}

func TestApplyStreamEvent_RefreshesSessionOnToolEvents(t *testing.T) {
	f := &FeishuChannel{chatStates: map[string]*chatState{}}
	cs := f.getChatState("oc1")
	s := &ChatSession{
		key:        "s1",
		lastActive: time.Now().Add(-2 * sessionExpiry),
		sem:        make(chan struct{}, 1),
	}
	cs.sessions["s1"] = s

	state := newCardState("pi")
	f.applyStreamEvent(cs, "s1", state, agent.StreamEvent{Type: "tool_result", ToolUseID: "call-1"})

	if time.Since(s.lastActive) > time.Minute {
		t.Errorf("tool events must refresh lastActive, got lastActive=%v", s.lastActive)
	}
}

func TestHandleMessage_RunContextHasNoWallClockDeadline(t *testing.T) {
	f, ch := newRPCTestChannel(t, "")

	msg := &types.NormalizedMessage{ChatID: "oc1", MessageID: "m1", Content: "问题"}
	if err := f.HandleMessage(context.Background(), ch, msg); err != nil {
		t.Fatalf("HandleMessage: %v", err)
	}

	// The dispatch is async; wait for the run to finish and close its stream.
	deadline := time.Now().Add(10 * time.Second)
	var ctrl *fakeStreamCtrl
	for time.Now().Before(deadline) {
		if c := ch.streamAt(0); c != nil {
			ctrl = c
			if c.isClosed() {
				break
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	if ctrl == nil {
		t.Fatal("expected a card stream to be started")
	}
	if !ctrl.isClosed() {
		t.Fatal("stream should be closed after the run")
	}

	// The watchdog (not a wall-clock deadline) must be the only bound on a run:
	// a deadline on the dispatched context cancels long runs with
	// DeadlineExceeded mid-flight.
	if _, hasDeadline := ctrl.streamCtx().Deadline(); hasDeadline {
		t.Error("run context must not carry a wall-clock deadline")
	}

	for _, text := range ch.sendTexts() {
		if strings.Contains(text, "任务执行失败") {
			t.Errorf("healthy run must not report a failure, got: %s", text)
		}
	}
}
