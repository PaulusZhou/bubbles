package agent

import (
	"context"
	"os"
	"strings"
	"sync"
	"testing"
	"time"
)

// fakeUIBridge records UI requests and answers with a configured responder.
type fakeUIBridge struct {
	mu      sync.Mutex
	reqs    []UIRequest
	respond func(UIRequest) *UIResponse
}

func (b *fakeUIBridge) HandleUIRequest(_ context.Context, req UIRequest) *UIResponse {
	b.mu.Lock()
	b.reqs = append(b.reqs, req)
	fn := b.respond
	b.mu.Unlock()
	if fn != nil {
		return fn(req)
	}
	return nil
}

func (b *fakeUIBridge) requests() []UIRequest {
	b.mu.Lock()
	defer b.mu.Unlock()
	return append([]UIRequest(nil), b.reqs...)
}

func startUIFake(t *testing.T) (*RPCSession, string) {
	t.Helper()
	respFile := t.TempDir() + "/ui_responses"
	t.Setenv("BUBBLES_TEST_UI_RESPONSES", respFile)
	t.Setenv("FAKE_PI_UI_DIALOG", "1")
	return startFakeRPC(t, fakeRPCPiBody), respFile
}

func waitResponseFile(t *testing.T, path string) string {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		data, err := os.ReadFile(path)
		if err == nil && len(data) > 0 {
			return string(data)
		}
		if time.Now().After(deadline) {
			t.Fatal("timed out waiting for extension_ui_response")
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestRPCSession_UIBridgeConfirm(t *testing.T) {
	s, respFile := startUIFake(t)
	bridge := &fakeUIBridge{respond: func(req UIRequest) *UIResponse {
		return NewUIConfirmResponse(req.ID, true)
	}}
	s.SetUIBridge(bridge)

	if err := s.RunPrompt(context.Background(), "hi", func(StreamEvent) error { return nil }); err != nil {
		t.Fatalf("RunPrompt: %v", err)
	}

	reqs := bridge.requests()
	if len(reqs) != 1 || reqs[0].Method != "confirm" || reqs[0].Title != "Allow?" {
		t.Fatalf("bridge requests = %+v, want one confirm dialog", reqs)
	}
	got := waitResponseFile(t, respFile)
	if !strings.Contains(got, `"confirmed":true`) || !strings.Contains(got, `"id":"ui-1"`) {
		t.Errorf("ui response = %s, want confirmed:true for ui-1", got)
	}
}

func TestRPCSession_UIBridgeCancel(t *testing.T) {
	s, respFile := startUIFake(t)
	s.SetUIBridge(&fakeUIBridge{}) // responder nil -> nil -> cancel

	if err := s.RunPrompt(context.Background(), "hi", func(StreamEvent) error { return nil }); err != nil {
		t.Fatalf("RunPrompt: %v", err)
	}

	got := waitResponseFile(t, respFile)
	if !strings.Contains(got, `"cancelled":true`) {
		t.Errorf("ui response = %s, want cancelled:true", got)
	}
}

func TestRPCSession_UINoBridgeAutoCancel(t *testing.T) {
	s, respFile := startUIFake(t)

	if err := s.RunPrompt(context.Background(), "hi", func(StreamEvent) error { return nil }); err != nil {
		t.Fatalf("RunPrompt: %v", err)
	}

	got := waitResponseFile(t, respFile)
	if !strings.Contains(got, `"cancelled":true`) {
		t.Errorf("ui response = %s, want auto-cancelled", got)
	}
}

func TestRPCSession_ForkAndGetForkMessages(t *testing.T) {
	s := startFakeRPC(t, fakeRPCPiBody)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	msgs, err := s.GetForkMessages(ctx)
	if err != nil {
		t.Fatalf("GetForkMessages: %v", err)
	}
	if len(msgs) != 2 || msgs[1].EntryID != "e2" {
		t.Errorf("fork messages = %+v, want two entries ending with e2", msgs)
	}

	text, cancelled, err := s.Fork(ctx, "e2")
	if err != nil {
		t.Fatalf("Fork: %v", err)
	}
	if cancelled || text != "问题二" {
		t.Errorf("Fork = (%q, %v), want (问题二, false)", text, cancelled)
	}
}
