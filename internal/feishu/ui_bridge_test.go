package feishu

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/larksuite/oapi-sdk-go/v3/channel/types"

	"github.com/pauluszhou/bubbles/internal/agent"
)

// waitSend polls the fake channel until its captured texts contain want.
func waitCardOrText(t *testing.T, ch *fakeChannel, want string) bool {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		ch.mu.Lock()
		found := false
		for _, s := range ch.sends {
			if strings.Contains(s.Text, want) || strings.Contains(s.Card, want) {
				found = true
				break
			}
		}
		ch.mu.Unlock()
		if found {
			return true
		}
		if time.Now().After(deadline) {
			return false
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestUIBridge_ConfirmFlow(t *testing.T) {
	f, ch := newRPCTestChannel(t, "")
	b := &uiBridge{f: f, chatID: "oc1"}

	respCh := make(chan *agent.UIResponse, 1)
	go func() {
		respCh <- b.HandleUIRequest(context.Background(), agent.UIRequest{
			ID: "r1", Method: "confirm", Title: "允许执行？", Message: "rm -rf /tmp",
		})
	}()

	if !waitCardOrText(t, ch, "允许执行？") {
		t.Fatal("confirm card was never sent")
	}
	if !waitCardOrText(t, ch, "ui_choice") {
		t.Fatal("confirm card should carry ui_choice buttons")
	}

	if !f.resolveUIChoice("r1", "yes") {
		t.Fatal("resolveUIChoice should find the pending request")
	}

	select {
	case resp := <-respCh:
		if resp == nil || resp.Confirmed == nil || !*resp.Confirmed {
			t.Fatalf("response = %+v, want confirmed:true", resp)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("dialog did not resolve")
	}
}

func TestUIBridge_SelectFlow(t *testing.T) {
	f, ch := newRPCTestChannel(t, "")
	b := &uiBridge{f: f, chatID: "oc1"}

	respCh := make(chan *agent.UIResponse, 1)
	go func() {
		respCh <- b.HandleUIRequest(context.Background(), agent.UIRequest{
			ID: "r2", Method: "select", Title: "选择方案", Options: []string{"方案 A", "方案 B"},
		})
	}()

	if !waitCardOrText(t, ch, "选择方案") {
		t.Fatal("select card was never sent")
	}
	f.resolveUIChoice("r2", "方案 B")

	select {
	case resp := <-respCh:
		if resp == nil || resp.Value == nil || *resp.Value != "方案 B" {
			t.Fatalf("response = %+v, want value 方案 B", resp)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("dialog did not resolve")
	}
}

func TestUIBridge_UnsupportedDialogsCancel(t *testing.T) {
	f, ch := newRPCTestChannel(t, "")
	b := &uiBridge{f: f, chatID: "oc1"}

	resp := b.HandleUIRequest(context.Background(), agent.UIRequest{
		ID: "r3", Method: "input", Title: "输入备注",
	})
	if resp != nil {
		t.Errorf("input dialog should be cancelled, got %+v", resp)
	}
	if !waitCardOrText(t, ch, "已自动取消") {
		t.Error("cancelling an input dialog should explain itself in chat")
	}
}

func TestUIBridge_Notify(t *testing.T) {
	f, ch := newRPCTestChannel(t, "")
	b := &uiBridge{f: f, chatID: "oc1"}

	resp := b.HandleUIRequest(context.Background(), agent.UIRequest{
		ID: "r4", Method: "notify", Message: "命令被拦截", NotifyType: "warning",
	})
	if resp != nil {
		t.Errorf("notify should return nil, got %+v", resp)
	}
	if !waitCardOrText(t, ch, "命令被拦截") {
		t.Error("notify should send a chat message")
	}
}

func TestBuildUIChoiceCard(t *testing.T) {
	card := BuildUIChoiceCard("工具确认", "是否允许 bash？", "req-9",
		[]uiChoice{{Label: "✅ 允许", Value: "yes"}, {Label: "❌ 拒绝", Value: "no"}})
	for _, want := range []string{"工具确认", "是否允许 bash？", "ui_choice", "req-9", "允许", "拒绝"} {
		if !strings.Contains(card, want) {
			t.Errorf("card missing %q: %s", want, card)
		}
	}
}

func TestBuildForkMessagesCard(t *testing.T) {
	msgs := []agent.ForkMessage{
		{EntryID: "e1", Text: "问题一\n第二行"},
		{EntryID: "e2", Text: "问题二"},
	}
	card := BuildForkMessagesCard(msgs, "s1")
	for _, want := range []string{"refork", "s1", "e1", "e2", "问题一", "问题二"} {
		if !strings.Contains(card, want) {
			t.Errorf("fork card missing %q: %s", want, card)
		}
	}
}

func TestRunRefork_RerunsForkedPrompt(t *testing.T) {
	f, ch := newRPCTestChannel(t, "")
	s := f.getOrCreateActiveSession("oc1")

	f.runRefork("oc1", s.key, "")

	if !waitCardOrText(t, ch, "已回到该提问") {
		t.Fatal("runRefork should announce the re-run")
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		if ctrl := ch.streamAt(0); ctrl != nil && strings.Contains(ctrl.lastUpdate(), "最终回复") {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("re-run never produced a final answer")
		}
		time.Sleep(10 * time.Millisecond)
	}
	if !strings.Contains(ch.streamAt(0).lastUpdate(), "重新回答") {
		t.Error("final card should offer reanswer again")
	}
}

func TestRunRefork_DeclinesWhileRunning(t *testing.T) {
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
	deadline := time.Now().Add(5 * time.Second)
	for {
		if ctrl := ch.streamAt(0); ctrl != nil && strings.Contains(ctrl.lastUpdate(), "答案") {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("first run never streamed text")
		}
		time.Sleep(10 * time.Millisecond)
	}

	f.runRefork("oc1", s.key, "")
	if !waitCardOrText(t, ch, "任务进行中") {
		t.Error("runRefork should decline while a run is active")
	}

	f.processMessageRPC(context.Background(), ch, &types.NormalizedMessage{
		ChatID: "oc1", MessageID: "m2", Content: "结束",
	}, s.key)
	select {
	case <-runDone:
	case <-time.After(5 * time.Second):
		t.Fatal("run did not settle")
	}
}
