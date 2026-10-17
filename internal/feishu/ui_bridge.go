package feishu

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/larksuite/oapi-sdk-go/v3/channel/types"

	"github.com/pauluszhou/bubbles/internal/agent"
)

// defaultUIChoiceTimeout bounds how long a UI dialog waits for a card answer
// when the request carries no timeout.
const defaultUIChoiceTimeout = 10 * time.Minute

// uiBridge forwards pi extension UI requests to Feishu cards for one chat.
// Confirmation and selection dialogs become cards with callback buttons;
// notifications become chat messages; text-input dialogs are declined because
// cards cannot return free-form text.
type uiBridge struct {
	f      *FeishuChannel
	chatID string
}

// HandleUIRequest implements agent.UIBridge.
func (b *uiBridge) HandleUIRequest(ctx context.Context, req agent.UIRequest) *agent.UIResponse {
	switch req.Method {
	case "confirm", "select":
		return b.resolveChoice(ctx, req)
	case "notify":
		text := "🔔 通知"
		if req.NotifyType != "" {
			text = "🔔 " + req.NotifyType
		}
		if req.Message != "" {
			text += ": " + req.Message
		}
		b.f.sendChatText(ctx, b.chatID, text)
		return nil
	case "input", "editor":
		b.f.sendChatText(ctx, b.chatID,
			fmt.Sprintf("💬 %s\n（该交互需要文本输入，飞书卡片暂不支持，已自动取消）", req.Title))
		return nil
	default:
		// setStatus / setWidget / setTitle / set_editor_text: 无对应展示，忽略。
		return nil
	}
}

// resolveChoice sends a card with one button per option and waits for a click.
func (b *uiBridge) resolveChoice(ctx context.Context, req agent.UIRequest) *agent.UIResponse {
	var choices []uiChoice
	if req.Method == "confirm" {
		choices = []uiChoice{{Label: "✅ 允许", Value: "yes"}, {Label: "❌ 拒绝", Value: "no"}}
	} else {
		for _, opt := range req.Options {
			choices = append(choices, uiChoice{Label: opt, Value: opt})
		}
	}
	if len(choices) == 0 {
		return nil
	}

	ch := b.f.registerUIChoice(req.ID)
	defer b.f.unregisterUIChoice(req.ID)

	card := BuildUIChoiceCard(req.Title, req.Message, req.ID, choices)
	if _, err := b.f.ch.Send(ctx, &types.SendInput{ChatID: b.chatID, Card: card}); err != nil {
		slog.Error("feishu: failed to send ui choice card",
			"chat_id", b.chatID, "request_id", req.ID, "error", err)
		return nil
	}
	slog.Info("feishu: ui choice card sent",
		"chat_id", b.chatID, "request_id", req.ID, "method", req.Method)

	timeout := defaultUIChoiceTimeout
	if req.Timeout > 0 {
		timeout = time.Duration(req.Timeout) * time.Millisecond
	}
	timer := time.NewTimer(timeout)
	defer timer.Stop()

	select {
	case choice := <-ch:
		slog.Info("feishu: ui choice resolved",
			"request_id", req.ID, "method", req.Method, "choice", choice)
		if req.Method == "confirm" {
			return agent.NewUIConfirmResponse(req.ID, choice == "yes")
		}
		return agent.NewUIValueResponse(req.ID, choice)
	case <-timer.C:
		slog.Info("feishu: ui choice timed out", "request_id", req.ID)
		b.f.sendChatText(ctx, b.chatID, "⌛ 交互超时，已自动取消")
		return nil
	case <-ctx.Done():
		return nil
	}
}

// registerUIChoice registers a pending dialog awaiting a card button click.
func (f *FeishuChannel) registerUIChoice(requestID string) chan string {
	ch := make(chan string, 1)
	f.uiChoicesMu.Lock()
	if f.uiChoices == nil {
		f.uiChoices = map[string]chan string{}
	}
	f.uiChoices[requestID] = ch
	f.uiChoicesMu.Unlock()
	return ch
}

// resolveUIChoice delivers a card button click to a pending dialog.
func (f *FeishuChannel) resolveUIChoice(requestID, choice string) bool {
	f.uiChoicesMu.Lock()
	ch, ok := f.uiChoices[requestID]
	delete(f.uiChoices, requestID)
	f.uiChoicesMu.Unlock()
	if !ok {
		return false
	}
	select {
	case ch <- choice:
	default:
	}
	return true
}

// unregisterUIChoice drops a pending dialog registration.
func (f *FeishuChannel) unregisterUIChoice(requestID string) {
	f.uiChoicesMu.Lock()
	delete(f.uiChoices, requestID)
	f.uiChoicesMu.Unlock()
}

// sendChatText sends a plain text message, logging failures.
func (f *FeishuChannel) sendChatText(ctx context.Context, chatID, text string) {
	if _, err := f.ch.Send(ctx, &types.SendInput{ChatID: chatID, Text: text}); err != nil {
		slog.Error("feishu: failed to send text", "chat_id", chatID, "error", err)
	}
}
