package feishu

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/larksuite/oapi-sdk-go/v3/channel/types"

	"github.com/pauluszhou/bubbles/internal/agent"
	"github.com/pauluszhou/bubbles/internal/config"
)

// fakeStreamCtrl records card updates from a streaming reply.
type fakeStreamCtrl struct {
	mu      sync.Mutex
	updates []string
	closed  bool
}

func (c *fakeStreamCtrl) Append(context.Context, string) error { return nil }
func (c *fakeStreamCtrl) Flush(context.Context) error          { return nil }
func (c *fakeStreamCtrl) UpdateCard(_ context.Context, s string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.updates = append(c.updates, s)
	return nil
}
func (c *fakeStreamCtrl) Close(context.Context) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.closed = true
	return nil
}

func (c *fakeStreamCtrl) lastUpdate() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.updates) == 0 {
		return ""
	}
	return c.updates[len(c.updates)-1]
}

func (c *fakeStreamCtrl) updateCount() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.updates)
}

// fakeChannel implements types.Channel for tests: it records sends and hands
// out fakeStreamCtrl instances. Unimplemented methods panic via the embedded
// nil interface if unexpectedly called.
type fakeChannel struct {
	types.Channel
	mu      sync.Mutex
	sends   []*types.SendInput
	streams []*fakeStreamCtrl
}

func (f *fakeChannel) Send(_ context.Context, in *types.SendInput) (*types.SendResult, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.sends = append(f.sends, in)
	return &types.SendResult{}, nil
}

func (f *fakeChannel) Stream(_ context.Context, in *types.SendInput) (types.StreamController, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	ctrl := &fakeStreamCtrl{}
	f.streams = append(f.streams, ctrl)
	return ctrl, nil
}

func (f *fakeChannel) sendTexts() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []string
	for _, s := range f.sends {
		out = append(out, s.Text)
	}
	return out
}

func (f *fakeChannel) streamCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.streams)
}

func (f *fakeChannel) streamAt(i int) *fakeStreamCtrl {
	f.mu.Lock()
	defer f.mu.Unlock()
	if i < 0 || i >= len(f.streams) {
		return nil
	}
	return f.streams[i]
}

// fakeRPCPiScript writes a pi RPC emulator script and returns its path.
func fakeRPCPiScript(t *testing.T) string {
	t.Helper()
	body := `echo '{"type":"session","id":"sess-rpc-1"}'
while IFS= read -r line; do
  id=$(printf '%s' "$line" | sed -n 's/.*"id":"\([^"]*\)".*/\1/p')
  cmd=$(printf '%s' "$line" | sed -n 's/.*"type":"\([^"]*\)".*/\1/p')
  case "$cmd" in
    prompt)
      echo "{\"id\":\"$id\",\"type\":\"response\",\"command\":\"prompt\",\"success\":true,\"data\":{\"disposition\":\"started\"}}"
      echo '{"type":"message_start","message":{"role":"assistant","content":[],"stopReason":"pending"}}'
      echo '{"type":"tool_execution_start","toolCallId":"call-1","toolName":"bash","args":{"command":"ls"}}'
      echo '{"type":"tool_execution_end","toolCallId":"call-1","toolName":"bash","result":{"content":[]},"isError":false}'
      echo '{"type":"message_update","assistantMessageEvent":{"type":"text_end","contentIndex":0,"content":"答案"}}'
      echo '{"type":"message_end","message":{"role":"assistant","content":[{"type":"text","text":"答案"}],"stopReason":"stop"}}'
      if [ -z "$FAKE_PI_DEFER_SETTLE" ]; then
        echo '{"type":"agent_settled","aborted":false}'
      fi
      ;;
    steer|follow_up)
      echo "{\"id\":\"$id\",\"type\":\"response\",\"command\":\"$cmd\",\"success\":true,\"data\":{\"disposition\":\"queued\"}}"
      if [ -n "$FAKE_PI_DEFER_SETTLE" ]; then
        echo '{"type":"agent_settled","aborted":false}'
      fi
      ;;
    get_fork_messages)
      echo "{\"id\":\"$id\",\"type\":\"response\",\"command\":\"get_fork_messages\",\"success\":true,\"data\":{\"messages\":[{\"entryId\":\"e1\",\"text\":\"问题一\"},{\"entryId\":\"e2\",\"text\":\"问题二\"}]}}"
      ;;
    fork)
      echo "{\"id\":\"$id\",\"type\":\"response\",\"command\":\"fork\",\"success\":true,\"data\":{\"text\":\"问题二\",\"cancelled\":false}}"
      ;;
    *)
      echo "{\"id\":\"$id\",\"type\":\"response\",\"command\":\"$cmd\",\"success\":true}"
      ;;
  esac
done`
	path := filepath.Join(t.TempDir(), "fake_pi_rpc.sh")
	if err := os.WriteFile(path, []byte("#!/bin/sh\n"+body), 0755); err != nil {
		t.Fatalf("write fake pi: %v", err)
	}
	return path
}

// newRPCTestChannel builds a FeishuChannel wired to the fake pi RPC script.
func newRPCTestChannel(t *testing.T, behavior string) (*FeishuChannel, *fakeChannel) {
	t.Helper()
	script := fakeRPCPiScript(t)
	f := &FeishuChannel{
		cfg:         &config.Config{Agent: "pi", PiPath: script, WorkDir: t.TempDir(), PiRPCBehavior: behavior},
		agentClient: agent.NewClient("pi", script, ""),
		rpc:         agent.NewRPCManager(script, t.TempDir(), "", time.Minute),
		chatStates:  map[string]*chatState{},
	}
	ch := &fakeChannel{}
	f.ch = ch
	t.Cleanup(func() { f.rpc.CloseAll() })
	return f, ch
}

func TestProcessMessageRPC_StreamsCardAndRecordsSession(t *testing.T) {
	f, ch := newRPCTestChannel(t, "")

	msg := &types.NormalizedMessage{ChatID: "oc1", MessageID: "m1", Content: "问题"}
	s := f.getOrCreateActiveSession("oc1")

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	f.processMessageRPC(ctx, ch, msg, s.key)

	if ch.streamCount() != 1 {
		t.Fatalf("expected 1 stream, got %d", ch.streamCount())
	}
	last := ch.streamAt(0).lastUpdate()
	if !strings.Contains(last, "最终回复") || !strings.Contains(last, "答案") {
		t.Errorf("final card should contain the answer, got: %s", last)
	}
	if !strings.Contains(last, "工具活动") {
		t.Errorf("final card should contain tool activity, got: %s", last)
	}
	if s.agentSID != "sess-rpc-1" {
		t.Errorf("agentSID = %q, want sess-rpc-1", s.agentSID)
	}
	if !ch.streamAt(0).closed {
		t.Error("stream should be closed after the run")
	}
}

func TestProcessMessageRPC_SteersIntoRunningRun(t *testing.T) {
	t.Setenv("FAKE_PI_DEFER_SETTLE", "1")
	f, ch := newRPCTestChannel(t, "")

	s := f.getOrCreateActiveSession("oc1")

	runDone := make(chan struct{})
	go func() {
		defer close(runDone)
		f.processMessageRPC(context.Background(), ch, &types.NormalizedMessage{
			ChatID: "oc1", MessageID: "m1", Content: "问题",
		}, s.key)
	}()

	// Wait until the first message's run streams its text.
	deadline := time.Now().Add(5 * time.Second)
	for {
		if s0 := ch.streamAt(0); s0 != nil && strings.Contains(s0.lastUpdate(), "答案") {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("first run never streamed text")
		}
		time.Sleep(10 * time.Millisecond)
	}

	// Second message while the run is active: it must be steered in, not run.
	f.processMessageRPC(context.Background(), ch, &types.NormalizedMessage{
		ChatID: "oc1", MessageID: "m2", Content: "换个方向",
	}, s.key)

	texts := ch.sendTexts()
	steered := false
	for _, txt := range texts {
		if strings.Contains(txt, "已引导") {
			steered = true
		}
	}
	if !steered {
		t.Errorf("expected a steer acknowledgement, sends: %v", texts)
	}
	if ch.streamCount() != 1 {
		t.Errorf("steered message must not open a second stream, got %d", ch.streamCount())
	}

	select {
	case <-runDone:
	case <-time.After(5 * time.Second):
		t.Fatal("first run did not settle after steer")
	}
}

func TestProcessMessageRPC_FollowUpBehavior(t *testing.T) {
	t.Setenv("FAKE_PI_DEFER_SETTLE", "1")
	f, ch := newRPCTestChannel(t, "follow_up")

	s := f.getOrCreateActiveSession("oc1")

	runDone := make(chan struct{})
	go func() {
		defer close(runDone)
		f.processMessageRPC(context.Background(), ch, &types.NormalizedMessage{
			ChatID: "oc1", MessageID: "m1", Content: "问题",
		}, s.key)
	}()

	deadline := time.Now().Add(5 * time.Second)
	for {
		if s0 := ch.streamAt(0); s0 != nil && strings.Contains(s0.lastUpdate(), "答案") {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("first run never streamed text")
		}
		time.Sleep(10 * time.Millisecond)
	}

	f.processMessageRPC(context.Background(), ch, &types.NormalizedMessage{
		ChatID: "oc1", MessageID: "m2", Content: "顺便总结",
	}, s.key)

	texts := ch.sendTexts()
	followed := false
	for _, txt := range texts {
		if strings.Contains(txt, "已排队") {
			followed = true
		}
	}
	if !followed {
		t.Errorf("expected a follow_up acknowledgement, sends: %v", texts)
	}

	// The fake settles on steer/follow_up; the run should end now.
	select {
	case <-runDone:
	case <-time.After(5 * time.Second):
		t.Fatal("first run did not settle after follow_up")
	}
}

func TestCloseSessionClosesRPCSession(t *testing.T) {
	f, _ := newRPCTestChannel(t, "")

	s := f.getOrCreateActiveSession("oc1")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	sess, err := f.rpc.Get(ctx, rpcKey("oc1", s.key), "")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}

	if err := f.CloseSession("oc1", s.key); err != nil {
		t.Fatalf("CloseSession: %v", err)
	}
	select {
	case <-sess.Done():
	case <-time.After(5 * time.Second):
		t.Fatal("CloseSession should close the pi RPC process")
	}
	if f.rpc.Peek(rpcKey("oc1", s.key)) != nil {
		t.Error("RPC session should be removed after CloseSession")
	}
}
