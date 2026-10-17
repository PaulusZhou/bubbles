package agent

import (
	"context"
	"strings"
	"testing"
	"time"
)

// fakeRPCPiBody is a shell script that emulates `pi --mode rpc`: it reads JSONL
// commands from stdin and answers with correlated responses plus canned
// session events. Like real pi RPC mode it emits no session header record —
// the session ID is only reported by get_state. Environment switches:
//
//	FAKE_PI_REJECT=1       -> prompt commands are rejected
//	FAKE_PI_DEFER_SETTLE=1 -> agent_settled is only emitted after a steer
//	FAKE_PI_UI_DIALOG=1    -> prompt emits a confirm dialog and settles on the response
const fakeRPCPiBody = `while IFS= read -r line; do
  id=$(printf '%s' "$line" | sed -n 's/.*"id":"\([^"]*\)".*/\1/p')
  cmd=$(printf '%s' "$line" | sed -n 's/.*"type":"\([^"]*\)".*/\1/p')
  case "$cmd" in
    prompt)
      if [ -n "$FAKE_PI_REJECT" ]; then
        echo "{\"id\":\"$id\",\"type\":\"response\",\"command\":\"prompt\",\"success\":false,\"error\":\"rejected by test\"}"
        continue
      fi
      echo "{\"id\":\"$id\",\"type\":\"response\",\"command\":\"prompt\",\"success\":true,\"data\":{\"disposition\":\"started\"}}"
      echo '{"type":"agent_start"}'
      echo '{"type":"message_start","message":{"role":"assistant","content":[],"stopReason":"pending"}}'
      echo '{"type":"tool_execution_start","toolCallId":"call-1","toolName":"bash","args":{"command":"ls"}}'
      echo '{"type":"tool_execution_end","toolCallId":"call-1","toolName":"bash","result":{"content":[]},"isError":false}'
      echo '{"type":"message_update","assistantMessageEvent":{"type":"text_delta","contentIndex":0,"delta":"hello"}}'
      echo '{"type":"message_update","assistantMessageEvent":{"type":"text_end","contentIndex":0,"content":"hello"}}'
      echo '{"type":"message_end","message":{"role":"assistant","content":[{"type":"text","text":"hello"}],"stopReason":"stop"}}'
      if [ -n "$FAKE_PI_UI_DIALOG" ]; then
        echo '{"type":"extension_ui_request","id":"ui-1","method":"confirm","title":"Allow?","message":"rm -rf /tmp","timeout":5000}'
      elif [ -z "$FAKE_PI_DEFER_SETTLE" ]; then
        echo '{"type":"agent_settled","aborted":false}'
      fi
      ;;
    extension_ui_response)
      if [ -n "$BUBBLES_TEST_UI_RESPONSES" ]; then
        printf '%s\n' "$line" >> "$BUBBLES_TEST_UI_RESPONSES"
      fi
      echo '{"type":"agent_settled","aborted":false}'
      ;;
    steer)
      echo "{\"id\":\"$id\",\"type\":\"response\",\"command\":\"steer\",\"success\":true,\"data\":{\"disposition\":\"queued\"}}"
      if [ -n "$FAKE_PI_DEFER_SETTLE" ]; then
        echo '{"type":"agent_settled","aborted":false}'
      fi
      ;;
    follow_up)
      echo "{\"id\":\"$id\",\"type\":\"response\",\"command\":\"follow_up\",\"success\":true,\"data\":{\"disposition\":\"queued\"}}"
      ;;
    abort)
      echo "{\"id\":\"$id\",\"type\":\"response\",\"command\":\"abort\",\"success\":true}"
      ;;
    get_state)
      echo "{\"id\":\"$id\",\"type\":\"response\",\"command\":\"get_state\",\"success\":true,\"data\":{\"sessionId\":\"sess-rpc-1\"}}"
      ;;
    get_fork_messages)
      echo "{\"id\":\"$id\",\"type\":\"response\",\"command\":\"get_fork_messages\",\"success\":true,\"data\":{\"messages\":[{\"entryId\":\"e1\",\"text\":\"问题一\"},{\"entryId\":\"e2\",\"text\":\"问题二\"}]}}"
      ;;
    fork)
      echo "{\"id\":\"$id\",\"type\":\"response\",\"command\":\"fork\",\"success\":true,\"data\":{\"text\":\"问题二\",\"cancelled\":false}}"
      ;;
    get_session_stats)
      echo "{\"id\":\"$id\",\"type\":\"response\",\"command\":\"get_session_stats\",\"success\":true,\"data\":{\"sessionId\":\"sess-rpc-1\",\"tokens\":{\"total\":42},\"cost\":0.5}}"
      ;;
    export_html)
      echo "{\"id\":\"$id\",\"type\":\"response\",\"command\":\"export_html\",\"success\":true,\"data\":{\"path\":\"/tmp/session.html\"}}"
      ;;
    *)
      echo "{\"id\":\"$id\",\"type\":\"response\",\"command\":\"$cmd\",\"success\":true}"
      ;;
  esac
done`

// startFakeRPC starts an RPCSession against the fake pi script.
func startFakeRPC(t *testing.T, body string) *RPCSession {
	t.Helper()
	script := writeFakePi(t, body)
	s := NewRPCSession(script, t.TempDir(), "", "")
	if err := s.Start(context.Background()); err != nil {
		t.Fatalf("start rpc session: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

func TestRPCSession_RunPrompt_MapsEvents(t *testing.T) {
	s := startFakeRPC(t, fakeRPCPiBody)

	var events []StreamEvent
	err := s.RunPrompt(context.Background(), "hi", func(ev StreamEvent) error {
		events = append(events, ev)
		return nil
	})
	if err != nil {
		t.Fatalf("RunPrompt: %v", err)
	}

	var types []string
	for _, ev := range events {
		types = append(types, ev.Type)
	}
	want := []string{"system", "tool_use", "tool_result", "text", "result"}
	if strings.Join(types, ",") != strings.Join(want, ",") {
		t.Errorf("event types = %v, want %v", types, want)
	}
	if events[0].SessionID != "sess-rpc-1" {
		t.Errorf("system event session id = %q, want sess-rpc-1", events[0].SessionID)
	}
	last := events[len(events)-1]
	if last.Type != "result" || last.Text != "hello" || last.IsError {
		t.Errorf("result event = %+v, want text %q", last, "hello")
	}
	if s.SessionID() != "sess-rpc-1" {
		t.Errorf("SessionID() = %q, want sess-rpc-1", s.SessionID())
	}
	if s.Running() {
		t.Error("session should not report a running run after settle")
	}
}

func TestRPCSession_SteerDuringRun(t *testing.T) {
	t.Setenv("FAKE_PI_DEFER_SETTLE", "1")
	s := startFakeRPC(t, fakeRPCPiBody)

	textSeen := make(chan struct{})
	var once bool
	runDone := make(chan error, 1)
	go func() {
		runDone <- s.RunPrompt(context.Background(), "hi", func(ev StreamEvent) error {
			if ev.Type == "text" && !once {
				once = true
				close(textSeen)
			}
			return nil
		})
	}()

	select {
	case <-textSeen:
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for text event")
	}

	if !s.Running() {
		t.Fatal("session should report a running run")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := s.Steer(ctx, "change direction"); err != nil {
		t.Fatalf("Steer: %v", err)
	}

	select {
	case err := <-runDone:
		if err != nil {
			t.Fatalf("RunPrompt: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("run did not settle after steer")
	}
}

func TestRPCSession_FollowUpRequiresRun(t *testing.T) {
	s := startFakeRPC(t, fakeRPCPiBody)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := s.FollowUp(ctx, "also this"); err == nil {
		t.Fatal("FollowUp without a run should fail")
	}
	if err := s.Steer(ctx, "change"); err == nil {
		t.Fatal("Steer without a run should fail")
	}
}

func TestRPCSession_ConcurrentRunRejected(t *testing.T) {
	t.Setenv("FAKE_PI_DEFER_SETTLE", "1")
	s := startFakeRPC(t, fakeRPCPiBody)

	textSeen := make(chan struct{})
	var once bool
	runDone := make(chan error, 1)
	go func() {
		runDone <- s.RunPrompt(context.Background(), "hi", func(ev StreamEvent) error {
			if ev.Type == "text" && !once {
				once = true
				close(textSeen)
			}
			return nil
		})
	}()
	select {
	case <-textSeen:
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for text event")
	}

	err := s.RunPrompt(context.Background(), "again", func(StreamEvent) error { return nil })
	if err == nil || !strings.Contains(err.Error(), "already in flight") {
		t.Errorf("second RunPrompt error = %v, want already in flight", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := s.Abort(ctx); err != nil {
		t.Fatalf("Abort: %v", err)
	}
	// The fake settles on steer, not abort; steer to finish the run.
	if err := s.Steer(ctx, "wrap up"); err != nil {
		t.Fatalf("Steer: %v", err)
	}
	select {
	case <-runDone:
	case <-time.After(5 * time.Second):
		t.Fatal("run did not settle")
	}
}

func TestRPCSession_PromptRejected(t *testing.T) {
	t.Setenv("FAKE_PI_REJECT", "1")
	s := startFakeRPC(t, fakeRPCPiBody)

	err := s.RunPrompt(context.Background(), "hi", func(StreamEvent) error { return nil })
	if err == nil || !strings.Contains(err.Error(), "rejected by test") {
		t.Fatalf("RunPrompt error = %v, want rejection", err)
	}
	if s.Running() {
		t.Error("session should be idle after a rejected prompt")
	}
}

func TestRPCSession_ProcessExit(t *testing.T) {
	s := startFakeRPC(t, "exit 1\n")

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	err := s.RunPrompt(ctx, "hi", func(StreamEvent) error { return nil })
	if err == nil {
		t.Fatal("expected error when the pi process exits")
	}
}

func TestRPCSession_GetStateAndStats(t *testing.T) {
	s := startFakeRPC(t, fakeRPCPiBody)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	state, err := s.GetState(ctx)
	if err != nil {
		t.Fatalf("GetState: %v", err)
	}
	if !strings.Contains(string(state), "sess-rpc-1") {
		t.Errorf("GetState = %s, want session id", state)
	}
	if _, err := s.GetSessionStats(ctx); err != nil {
		t.Fatalf("GetSessionStats: %v", err)
	}
}

func TestRPCSession_SessionIDFromGetState(t *testing.T) {
	// Real pi RPC mode emits no session header record; the session ID must
	// come from get_state so the host can persist it for resume and refork.
	s := startFakeRPC(t, fakeRPCPiBody)

	if s.SessionID() != "sess-rpc-1" {
		t.Errorf("SessionID() = %q, want sess-rpc-1", s.SessionID())
	}
}

func TestRPCSession_SurvivesCallerContextCancel(t *testing.T) {
	// The subprocess must outlive the request that started it: bubblesd
	// starts pooled sessions from per-message contexts whose cancellation
	// must not kill pi mid-conversation.
	script := writeFakePi(t, fakeRPCPiBody)
	s := NewRPCSession(script, t.TempDir(), "", "")
	startCtx, cancelStart := context.WithCancel(context.Background())
	if err := s.Start(startCtx); err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	cancelStart()

	runCtx, cancelRun := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancelRun()
	if err := s.RunPrompt(runCtx, "hi", func(StreamEvent) error { return nil }); err != nil {
		t.Fatalf("RunPrompt after caller ctx cancel: %v", err)
	}
}
