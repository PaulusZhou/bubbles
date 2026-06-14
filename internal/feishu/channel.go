package feishu

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	lark "github.com/larksuite/oapi-sdk-go/v3"
	"github.com/larksuite/oapi-sdk-go/v3/channel"
	"github.com/larksuite/oapi-sdk-go/v3/channel/types"
	larkcore "github.com/larksuite/oapi-sdk-go/v3/core"
	larkdispatcher "github.com/larksuite/oapi-sdk-go/v3/event/dispatcher"
	larkws "github.com/larksuite/oapi-sdk-go/v3/ws"

	"github.com/pauluszhou/bubbles/internal/agent"
	"github.com/pauluszhou/bubbles/internal/config"
	"github.com/pauluszhou/bubbles/internal/daemon"
)

// chatSession tracks the Claude session ID for a Feishu chat.
type chatSession struct {
	sessionID  string
	lastActive time.Time
}

// sessionExpiry is how long a session can be idle before being discarded.
const sessionExpiry = 30 * time.Minute

// CommandHandler handles a Feishu slash command.
type CommandHandler func(ctx context.Context, ch types.Channel, msg *types.NormalizedMessage) error

// FeishuChannel wraps the SDK Channel and manages the Feishu bot lifecycle.
type FeishuChannel struct {
	ch              types.Channel
	cfg             *config.Config
	commands        map[string]CommandHandler // prefix -> handler
	defaultChatID    string
	scheduler       *daemon.Scheduler // for direct task operations on card callbacks

	sessions   map[string]*chatSession // chatID -> active Claude session
	sessionsMu sync.Mutex
}

// New creates a FeishuChannel from the given config.
func New(cfg *config.Config) *FeishuChannel {
	if cfg.FeishuAppID == "" || cfg.FeishuAppSecret == "" {
		slog.Info("feishu channel disabled", "reason", "missing app_id or app_secret")
		return nil
	}

	slog.Info("initializing feishu channel", "app_id", cfg.FeishuAppID)

	larkClient := lark.NewClient(cfg.FeishuAppID, cfg.FeishuAppSecret,
		lark.WithLogLevel(larkcore.LogLevelInfo),
	)

	eventDispatcher := larkdispatcher.NewEventDispatcher("", "")

	wsClient := larkws.NewClient(cfg.FeishuAppID, cfg.FeishuAppSecret,
		larkws.WithLogLevel(larkcore.LogLevelInfo),
		larkws.WithEventHandler(eventDispatcher),
	)

	ch := channel.NewChannel(larkClient, wsClient)
	slog.Info("feishu channel instance created")

	return &FeishuChannel{
		ch:           ch,
		cfg:          cfg,
		commands:     make(map[string]CommandHandler),
		defaultChatID: cfg.FeishuChatID,
		sessions:     make(map[string]*chatSession),
	}
}

// RegisterCommand registers a handler for a command prefix (e.g. "/cron").
func (f *FeishuChannel) RegisterCommand(prefix string, handler CommandHandler) {
	f.commands[prefix] = handler
	slog.Info("feishu: command registered", "prefix", prefix)
}

// SetScheduler sets the scheduler for direct task operations (pause/resume/delete).
// Must be called before Start().
func (f *FeishuChannel) SetScheduler(scheduler *daemon.Scheduler) {
	f.scheduler = scheduler
}

// NotifyTaskCompletion sends a task completion notification to Feishu as a v2 card.
// Implements daemon.FeishuNotifier.
func (f *FeishuChannel) NotifyTaskCompletion(ctx context.Context, completion daemon.TaskCompletion) error {
	if f.defaultChatID == "" {
		slog.Debug("feishu: no default chat_id configured, skipping notification",
			"task_id", completion.TaskID,
			"task_name", completion.TaskName,
		)
		return nil
	}

	cardJSON := BuildTaskCompletionCard(completion)

	_, err := f.ch.Send(ctx, &types.SendInput{
		ChatID: f.defaultChatID,
		Card:   cardJSON,
	})
	if err != nil {
		slog.Error("feishu: failed to send task completion notification",
			"task_id", completion.TaskID,
			"error", err,
		)
		return err
	}
	slog.Info("feishu: task completion notification sent",
		"task_id", completion.TaskID,
	)
	return nil
}

// getResumeSessionID returns the Claude session ID for a chat, or "" if expired/not found.
func (f *FeishuChannel) getResumeSessionID(chatID string) string {
	f.sessionsMu.Lock()
	defer f.sessionsMu.Unlock()
	s, ok := f.sessions[chatID]
	if !ok {
		return ""
	}
	if time.Since(s.lastActive) > sessionExpiry {
		delete(f.sessions, chatID)
		slog.Info("feishu: session expired", "chat_id", chatID)
		return ""
	}
	return s.sessionID
}

// updateSession stores or updates the Claude session ID for a chat.
func (f *FeishuChannel) updateSession(chatID, sessionID string) {
	f.sessionsMu.Lock()
	defer f.sessionsMu.Unlock()
	f.sessions[chatID] = &chatSession{
		sessionID:  sessionID,
		lastActive: time.Now(),
	}
	slog.Info("feishu: session updated", "chat_id", chatID, "session_id", sessionID)
}

// touchSession updates the last-active time for a chat session.
func (f *FeishuChannel) touchSession(chatID string) {
	f.sessionsMu.Lock()
	defer f.sessionsMu.Unlock()
	if s, ok := f.sessions[chatID]; ok {
		s.lastActive = time.Now()
	}
}

// ClearSession removes the Claude session for a chat, forcing the next message to start fresh.
func (f *FeishuChannel) ClearSession(chatID string) {
	f.sessionsMu.Lock()
	defer f.sessionsMu.Unlock()
	delete(f.sessions, chatID)
	slog.Info("feishu: session cleared", "chat_id", chatID)
}

// cleanupSessions removes idle sessions that have exceeded the expiry threshold.
func (f *FeishuChannel) cleanupSessions() {
	f.sessionsMu.Lock()
	defer f.sessionsMu.Unlock()
	now := time.Now()
	for chatID, s := range f.sessions {
		if now.Sub(s.lastActive) > sessionExpiry {
			delete(f.sessions, chatID)
			slog.Info("feishu: session cleaned up", "chat_id", chatID)
		}
	}
}

// Start registers event handlers and starts the WebSocket connection.
func (f *FeishuChannel) Start(ctx context.Context) error {
	ch := f.ch

	ch.OnReady(func() {
		slog.Info("feishu channel ready")
	})
	ch.OnError(func(err error) {
		slog.Error("feishu channel error", "error", err)
	})
	ch.OnReconnecting(func() {
		slog.Warn("feishu channel reconnecting")
	})
	ch.OnReconnected(func() {
		slog.Info("feishu channel reconnected")
	})
	ch.OnDisconnected(func() {
		slog.Warn("feishu channel disconnected")
	})

	ch.OnMessage(func(ctx context.Context, msg *types.NormalizedMessage) error {
		return f.HandleMessage(ctx, ch, msg)
	})
	slog.Info("feishu message handler registered")

	// Register card action handler for interactive task cards
	if f.scheduler != nil {
		ch.OnCardAction(func(ctx context.Context, event *types.CardActionEvent) error {
			return f.handleCardAction(ctx, event)
		})
		slog.Info("feishu card action handler registered")
	}

	go func() {
		slog.Info("feishu channel connecting to websocket...")
		if err := ch.Start(ctx); err != nil {
			slog.Error("feishu channel start failed", "error", err)
		}
	}()

	// Periodically clean up idle sessions
	go func() {
		ticker := time.NewTicker(5 * time.Minute)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				f.cleanupSessions()
			}
		}
	}()

	return nil
}

// Stop gracefully stops the Feishu Channel.
func (f *FeishuChannel) Stop(ctx context.Context) error {
	slog.Info("stopping feishu channel")
	if f.ch != nil {
		err := f.ch.Stop(ctx)
		if err != nil {
			slog.Error("failed to stop feishu channel", "error", err)
			return err
		}
		slog.Info("feishu channel stopped")
	}
	return nil
}

// HandleMessage processes an incoming Feishu message.
// Uses card streaming via UpdateCard() to keep thinking and intermediate text
// in a single card that updates in place. Resumes the Claude session if one
// exists for this chat and hasn't expired.
func (f *FeishuChannel) HandleMessage(ctx context.Context, ch types.Channel, msg *types.NormalizedMessage) error {
	slog.Info("received feishu message",
		"chat_id", msg.ChatID,
		"chat_type", msg.ChatType,
		"user_id", msg.UserID,
		"content", msg.Content,
		"message_id", msg.MessageID,
	)

	// 命令分发：去掉 mention 前缀后检测命令（群聊中 @机器人 会产生 @_user_1 等占位符）
	trimmed := stripMentionPrefix(msg.Content)
	for prefix, handler := range f.commands {
		if trimmed == prefix || strings.HasPrefix(trimmed, prefix+" ") {
			return handler(ctx, ch, msg)
		}
	}

	resumeSessionID := f.getResumeSessionID(msg.ChatID)
	if resumeSessionID != "" {
		slog.Info("feishu: resuming session", "chat_id", msg.ChatID, "session_id", resumeSessionID)
	} else {
		slog.Info("feishu: new session", "chat_id", msg.ChatID)
	}

	state := newCardState()

	streamCtrl, err := ch.Stream(ctx, &types.SendInput{
		ChatID:         msg.ChatID,
		ReplyMessageID: msg.MessageID,
		Card:          state.BuildCard(),
	})
	if err != nil {
		slog.Error("feishu: failed to start card stream", "chat_id", msg.ChatID, "error", err)
		return err
	}

	err = agent.ClaudeStreamWithEventsTimeout(f.cfg.ClaudePath, msg.Content, f.cfg.WorkDir, resumeSessionID, func(event agent.StreamEvent) error {
		slog.Debug("feishu: stream event", "type", event.Type, "text_len", len(event.Text), "text_preview", func() string {
			if len(event.Text) > 150 {
				return event.Text[:150]
			}
			return event.Text
		}())
		switch event.Type {
		case "system":
			if event.SessionID != "" {
				f.updateSession(msg.ChatID, event.SessionID)
			}
		case "thinking":
			state.SetThinking(event.Text)
		case "result":
			state.SetFinal(event.Text)
			f.touchSession(msg.ChatID)
		}
		return streamCtrl.UpdateCard(ctx, state.BuildCard())
	})

	streamCtrl.Close(ctx)

	if err != nil {
		slog.Error("feishu: claude stream failed", "chat_id", msg.ChatID, "error", err)
		return err
	}

	// Log final card for debugging
	finalCard := state.BuildCard()
	if len(finalCard) > 3000 {
		slog.Info("feishu: final card JSON", "preview", finalCard[:3000])
	} else {
		slog.Info("feishu: final card JSON", "card", finalCard)
	}

	slog.Info("feishu: card stream completed", "chat_id", msg.ChatID)
	return nil
}

// fallbackSend sends Claude output as a single message when streaming fails.
func fallbackSend(ctx context.Context, ch types.Channel, msg *types.NormalizedMessage, cfg *config.Config) error {
	slog.Info("feishu: using fallback mode",
		"chat_id", msg.ChatID,
		"reply_to", msg.MessageID,
	)

	result := agent.ClaudeWithTimeout(msg.Content, cfg.WorkDir)
	output := result.Output
	if result.Error != nil {
		slog.Error("feishu: claude execution failed in fallback mode",
			"chat_id", msg.ChatID,
			"error", result.Error,
		)
		output = fmt.Sprintf("%s\n❌ 执行出错: %v", output, result.Error)
	}

	// Split long output into chunks if needed (Feishu has a ~4000 char limit per message)
	chunks := splitMessage(output, 3500)
	for i, chunk := range chunks {
		sendResult, err := ch.Send(ctx, &types.SendInput{
			ChatID:         msg.ChatID,
			Markdown:       chunk,
			ReplyMessageID: msg.MessageID,
		})
		if err != nil {
			slog.Error("feishu: failed to send fallback message",
				"chat_id", msg.ChatID,
				"chunk", fmt.Sprintf("%d/%d", i+1, len(chunks)),
				"error", err,
			)
			return err
		}
		slog.Info("feishu: fallback message sent",
			"chat_id", msg.ChatID,
			"chunk", fmt.Sprintf("%d/%d", i+1, len(chunks)),
			"message_id", sendResult.MessageID,
		)
	}

	return nil
}

// handleCardAction processes button clicks on interactive task cards.
func (f *FeishuChannel) handleCardAction(ctx context.Context, event *types.CardActionEvent) error {
	action, _ := event.Action.Value["action"].(string)
	taskID, _ := event.Action.Value["task_id"].(string)

	slog.Info("feishu: card action received",
		"action", action,
		"task_id", taskID,
		"chat_id", event.ChatID,
		"operator", event.Operator.OpenID,
		"form_value", fmt.Sprintf("%v", event.Action.FormValue),
	)

	if action == "" {
		return nil
	}

	// Handle create_task form submission (no taskID required)
	if action == "create_task" {
		return f.handleCreateTaskAction(ctx, event)
	}

	if taskID == "" {
		return nil
	}

	var toastMsg string
	switch action {
	case "pause":
		if err := f.scheduler.PauseTask(taskID); err != nil {
			slog.Error("feishu: pause task failed", "task_id", taskID, "error", err)
			toastMsg = fmt.Sprintf("❌ 暂停失败: %v", err)
		} else {
			toastMsg = "✅ 任务已暂停"
		}
	case "resume":
		if err := f.scheduler.ResumeTask(taskID); err != nil {
			slog.Error("feishu: resume task failed", "task_id", taskID, "error", err)
			toastMsg = fmt.Sprintf("❌ 恢复失败: %v", err)
		} else {
			toastMsg = "✅ 任务已恢复"
		}
	case "delete":
		if err := f.scheduler.DeleteTask(taskID); err != nil {
			slog.Error("feishu: delete task failed", "task_id", taskID, "error", err)
			toastMsg = fmt.Sprintf("❌ 删除失败: %v", err)
		} else {
			toastMsg = "🗑️ 任务已删除"
		}
	default:
		slog.Warn("feishu: unknown card action", "action", action)
		return nil
	}

	// Send a follow-up message to acknowledge the action
	_, err := f.ch.Send(ctx, &types.SendInput{
		ChatID: event.ChatID,
		Text:   toastMsg,
	})
	if err != nil {
		slog.Error("feishu: failed to send action acknowledgment", "error", err)
	}
	return nil
}

// parseFormString extracts a string from form value (handles string and []interface{}).
func parseFormString(fv map[string]interface{}, key string) string {
	v, ok := fv[key]
	if !ok {
		return ""
	}
	// multi_select returns []interface{}, single value returns string
	if arr, ok := v.([]interface{}); ok && len(arr) > 0 {
		s, _ := arr[0].(string)
		return s
	}
	s, _ := v.(string)
	return s
}

// stripMentionPrefix removes leading @_user_N mention placeholders from message content.
// In Feishu group chats, @mentioning the bot produces content like "@_user_1 /new".
func stripMentionPrefix(content string) string {
	s := strings.TrimSpace(content)
	for {
		if !strings.HasPrefix(s, "@_user_") {
			break
		}
		// find end of the @_user_N token
		end := strings.IndexAny(s[6:], " \t\n")
		if end == -1 {
			// entire string is just the mention
			return ""
		}
		s = strings.TrimSpace(s[6+end:])
	}
	return s
}

// buildCronFromForm generates a cron expression from form dropdown values.
// dayValue prefixes: "w0"-"w6" for weekday, "d1"-"d28" for month day.
func buildCronFromForm(freqType, dayValue, hourValue string) string {
	hour := hourValue
	if hour == "" {
		hour = "9"
	}

	switch freqType {
	case "weekdays":
		return fmt.Sprintf("0 %s * * 1-5", hour)
	case "weekly":
		day := strings.TrimPrefix(dayValue, "w")
		if day == "" {
			day = "1"
		}
		return fmt.Sprintf("0 %s * * %s", hour, day)
	case "monthly":
		day := strings.TrimPrefix(dayValue, "d")
		if day == "" {
			day = "1"
		}
		return fmt.Sprintf("0 %s %s * *", hour, day)
	default: // daily
		return fmt.Sprintf("0 %s * * *", hour)
	}
}

// handleCreateTaskAction processes the /cron-new form submission.
func (f *FeishuChannel) handleCreateTaskAction(ctx context.Context, event *types.CardActionEvent) error {
	fv := event.Action.FormValue

	prompt, _ := fv["prompt"].(string)
	freqType := parseFormString(fv, "freq_type")
	dayValue := parseFormString(fv, "day_value")
	hourValue := parseFormString(fv, "hour_value")

	if prompt == "" {
		_, _ = f.ch.Send(ctx, &types.SendInput{ChatID: event.ChatID, Text: "❌ 任务描述不能为空"})
		return nil
	}

	cronExpr := buildCronFromForm(freqType, dayValue, hourValue)

	slog.Info("feishu: creating task from form",
		"freq_type", freqType,
		"day_value", dayValue,
		"hour_value", hourValue,
		"cron_expr", cronExpr,
		"prompt", prompt,
		"chat_id", event.ChatID,
	)

	claudePrompt := buildCreateTaskPrompt("", cronExpr, prompt)

	// Run Claude asynchronously so we can return immediately (Feishu card action has ~5s timeout)
	go f.runCreateTaskStream(event.ChatID, claudePrompt)

	return nil
}

// runCreateTaskStream executes Claude in the background and streams results via card update.
func (f *FeishuChannel) runCreateTaskStream(chatID, claudePrompt string) {
	ctx := context.Background()

	state := newCardState()
	streamCtrl, err := f.ch.Stream(ctx, &types.SendInput{
		ChatID: chatID,
		Card:   state.BuildCard(),
	})
	if err != nil {
		slog.Error("feishu: failed to start card stream for create task", "error", err)
		return
	}

	err = agent.ClaudeStreamWithEventsTimeout(f.cfg.ClaudePath, claudePrompt, f.cfg.WorkDir, "", func(event agent.StreamEvent) error {
		switch event.Type {
		case "thinking":
			state.SetThinking(event.Text)
		case "result":
			state.SetFinal(event.Text)
		}
		return streamCtrl.UpdateCard(ctx, state.BuildCard())
	})

	streamCtrl.Close(ctx)

	if err != nil {
		slog.Error("feishu: claude stream failed for create task", "chat_id", chatID, "error", err)
		return
	}

	slog.Info("feishu: create task stream completed", "chat_id", chatID)
}


