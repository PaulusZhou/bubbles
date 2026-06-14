package feishu

import (
	"context"
	"fmt"
	"log/slog"
	"sort"
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

// ChatSession tracks a single Claude conversation within a Feishu chat.
type ChatSession struct {
	key          string    // unique key, e.g. "s1", "s2"
	name         string    // display name, e.g. "会话 1" or user-provided
	claudeSID    string    // Claude session ID for --resume
	firstMessage string    // user's first message in this session (for display)
	created      time.Time
	lastActive   time.Time
	sem          chan struct{} // capacity-1 semaphore to serialize messages within this session
}

// Name returns the display name of the session.
func (s *ChatSession) Name() string { return s.name }

// chatState holds all sessions for a single Feishu chat.
type chatState struct {
	sessions  map[string]*ChatSession // sessionKey -> session
	activeKey string                  // current active session key
	nextID    int                     // auto-increment counter for session keys
	mu        sync.Mutex
}

// sessionExpiry is how long a session can be idle before being discarded.
const sessionExpiry = 60 * time.Minute

// CommandHandler handles a Feishu slash command.
type CommandHandler func(ctx context.Context, ch types.Channel, msg *types.NormalizedMessage) error

// FeishuChannel wraps the SDK Channel and manages the Feishu bot lifecycle.
type FeishuChannel struct {
	ch            types.Channel
	cfg           *config.Config
	commands      map[string]CommandHandler // prefix -> handler
	defaultChatID string
	scheduler     *daemon.Scheduler // for direct task operations on card callbacks

	chatStates   map[string]*chatState // chatID -> chat state
	chatStatesMu sync.Mutex
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
		ch:            ch,
		cfg:           cfg,
		commands:      make(map[string]CommandHandler),
		defaultChatID: cfg.FeishuChatID,
		chatStates:    make(map[string]*chatState),
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

	cards := BuildTaskCompletionCard(completion)

	for i, cardJSON := range cards {
		_, err := f.ch.Send(ctx, &types.SendInput{
			ChatID: f.defaultChatID,
			Card:   cardJSON,
		})
		if err != nil {
			slog.Error("feishu: failed to send task completion notification",
				"task_id", completion.TaskID, "card_index", i,
				"error", err,
			)
			return err
		}
	}
	slog.Info("feishu: task completion notification sent",
		"task_id", completion.TaskID, "card_count", len(cards),
	)
	return nil
}

// getChatState returns the chat state for a chatID, creating it if needed.
func (f *FeishuChannel) getChatState(chatID string) *chatState {
	f.chatStatesMu.Lock()
	defer f.chatStatesMu.Unlock()
	cs, ok := f.chatStates[chatID]
	if !ok {
		cs = &chatState{
			sessions: make(map[string]*ChatSession),
		}
		f.chatStates[chatID] = cs
	}
	return cs
}

// getOrCreateActiveSession returns the active session for a chat, creating a default one if needed.
func (f *FeishuChannel) getOrCreateActiveSession(chatID string) *ChatSession {
	cs := f.getChatState(chatID)
	cs.mu.Lock()
	defer cs.mu.Unlock()

	if cs.activeKey != "" {
		if s, ok := cs.sessions[cs.activeKey]; ok {
			return s
		}
	}

	// No active session — create a default one
	cs.nextID++
	key := fmt.Sprintf("s%d", cs.nextID)
	s := &ChatSession{
		key:        key,
		name:       fmt.Sprintf("会话 %d", cs.nextID),
		created:    time.Now(),
		lastActive: time.Now(),
		sem:        make(chan struct{}, 1),
	}
	cs.sessions[key] = s
	cs.activeKey = key
	slog.Info("feishu: default session created", "chat_id", chatID, "session_key", key, "name", s.name)
	return s
}

// getResumeSessionID returns the Claude session ID for a session, or "" if expired/not found.
func (f *FeishuChannel) getResumeSessionID(chatID, sessionKey string) string {
	cs := f.getChatState(chatID)
	cs.mu.Lock()
	defer cs.mu.Unlock()
	s, ok := cs.sessions[sessionKey]
	if !ok {
		return ""
	}
	if time.Since(s.lastActive) > sessionExpiry {
		delete(cs.sessions, sessionKey)
		slog.Info("feishu: session expired", "chat_id", chatID, "session_key", sessionKey)
		return ""
	}
	return s.claudeSID
}

// updateSession stores or updates the Claude session ID for a session.
func (f *FeishuChannel) updateSession(chatID, sessionKey, claudeSID string) {
	cs := f.getChatState(chatID)
	cs.mu.Lock()
	defer cs.mu.Unlock()
	if s, ok := cs.sessions[sessionKey]; ok {
		s.claudeSID = claudeSID
		s.lastActive = time.Now()
		slog.Info("feishu: session updated", "chat_id", chatID, "session_key", sessionKey, "claude_sid", claudeSID)
	}
}

// touchSession updates the last-active time for a session.
func (f *FeishuChannel) touchSession(chatID, sessionKey string) {
	cs := f.getChatState(chatID)
	cs.mu.Lock()
	defer cs.mu.Unlock()
	if s, ok := cs.sessions[sessionKey]; ok {
		s.lastActive = time.Now()
	}
}

// NewSession creates a new session for a chat and sets it as active.
func (f *FeishuChannel) NewSession(chatID, name string) *ChatSession {
	cs := f.getChatState(chatID)
	cs.mu.Lock()
	defer cs.mu.Unlock()

	cs.nextID++
	key := fmt.Sprintf("s%d", cs.nextID)
	if name == "" {
		name = fmt.Sprintf("会话 %d", cs.nextID)
	}
	s := &ChatSession{
		key:        key,
		name:       name,
		created:    time.Now(),
		lastActive: time.Now(),
		sem:        make(chan struct{}, 1),
	}
	cs.sessions[key] = s
	cs.activeKey = key
	slog.Info("feishu: new session created", "chat_id", chatID, "session_key", key, "name", name)
	return s
}

// SwitchSession switches the active session for a chat.
func (f *FeishuChannel) SwitchSession(chatID, sessionKey string) error {
	cs := f.getChatState(chatID)
	cs.mu.Lock()
	defer cs.mu.Unlock()
	if _, ok := cs.sessions[sessionKey]; !ok {
		return fmt.Errorf("session %s not found", sessionKey)
	}
	cs.activeKey = sessionKey
	slog.Info("feishu: session switched", "chat_id", chatID, "session_key", sessionKey)
	return nil
}

// CloseSession removes a session from a chat. If it was active, switch to the most recent remaining session.
func (f *FeishuChannel) CloseSession(chatID, sessionKey string) error {
	cs := f.getChatState(chatID)
	cs.mu.Lock()
	defer cs.mu.Unlock()
	if _, ok := cs.sessions[sessionKey]; !ok {
		return fmt.Errorf("session %s not found", sessionKey)
	}
	delete(cs.sessions, sessionKey)

	// If this was the active session, switch to the most recent remaining
	if cs.activeKey == sessionKey {
		cs.activeKey = ""
		if latest := cs.mostRecentSession(); latest != nil {
			cs.activeKey = latest.key
			slog.Info("feishu: active session switched after close", "chat_id", chatID, "new_active", latest.key)
		}
	}
	slog.Info("feishu: session closed", "chat_id", chatID, "session_key", sessionKey)
	return nil
}

// GetSessions returns all sessions for a chat and the active key.
func (f *FeishuChannel) GetSessions(chatID string) ([]*ChatSession, string) {
	cs := f.getChatState(chatID)
	cs.mu.Lock()
	defer cs.mu.Unlock()
	sessions := make([]*ChatSession, 0, len(cs.sessions))
	for _, s := range cs.sessions {
		sessions = append(sessions, s)
	}
	sort.Slice(sessions, func(i, j int) bool {
		return sessions[i].created.Before(sessions[j].created)
	})
	return sessions, cs.activeKey
}

// mostRecentSession returns the session with the most recent lastActive time, or nil if empty.
// Caller must hold cs.mu.
func (cs *chatState) mostRecentSession() *ChatSession {
	var latest *ChatSession
	for _, s := range cs.sessions {
		if latest == nil || s.lastActive.After(latest.lastActive) {
			latest = s
		}
	}
	return latest
}

// cleanupSessions removes idle sessions that have exceeded the expiry threshold.
func (f *FeishuChannel) cleanupSessions() {
	f.chatStatesMu.Lock()
	chatIDs := make([]string, 0, len(f.chatStates))
	for chatID := range f.chatStates {
		chatIDs = append(chatIDs, chatID)
	}
	f.chatStatesMu.Unlock()

	now := time.Now()
	for _, chatID := range chatIDs {
		cs := f.getChatState(chatID)
		cs.mu.Lock()
		activeExpired := false
		for key, s := range cs.sessions {
			if now.Sub(s.lastActive) > sessionExpiry {
				delete(cs.sessions, key)
				slog.Info("feishu: session cleaned up", "chat_id", chatID, "session_key", key)
				if cs.activeKey == key {
					activeExpired = true
				}
			}
		}
		if activeExpired {
			cs.activeKey = ""
			if latest := cs.mostRecentSession(); latest != nil {
				cs.activeKey = latest.key
				slog.Info("feishu: active session switched after cleanup", "chat_id", chatID, "new_active", latest.key)
			}
		}
		cs.mu.Unlock()
	}
}

// truncateRunes truncates a string to at most n runes, appending "…" if truncated.
func truncateRunes(s string, n int) string {
	runes := []rune(s)
	if len(runes) <= n {
		return s
	}
	return string(runes[:n]) + "…"
}

// processMessage handles a single message with Claude streaming in its own goroutine.
func (f *FeishuChannel) processMessage(ctx context.Context, ch types.Channel, msg *types.NormalizedMessage, sessionKey string) {
	chatID := msg.ChatID
	cs := f.getChatState(chatID)

	slog.Info("feishu: processing message",
		"chat_id", chatID,
		"session_key", sessionKey,
		"message_id", msg.MessageID,
		"content", msg.Content,
	)

	resumeSessionID := f.getResumeSessionID(chatID, sessionKey)
	if resumeSessionID != "" {
		slog.Info("feishu: resuming session", "chat_id", chatID, "session_key", sessionKey, "session_id", resumeSessionID)
	} else {
		slog.Info("feishu: new session", "chat_id", chatID, "session_key", sessionKey)
	}

	// Get session name for card header, store first message if new
	cs.mu.Lock()
	sessionName := ""
	if s, ok := cs.sessions[sessionKey]; ok {
		sessionName = s.name
		if s.firstMessage == "" {
			s.firstMessage = truncateRunes(msg.Content, 100)
		}
	}
	cs.mu.Unlock()

	state := newCardState()

	streamCtrl, err := ch.Stream(ctx, &types.SendInput{
		ChatID:         chatID,
		ReplyMessageID: msg.MessageID,
		Card:           state.BuildCard(),
	})
	if err != nil {
		slog.Error("feishu: failed to start card stream", "chat_id", chatID, "error", err)
		return
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
				cs.mu.Lock()
				if s, ok := cs.sessions[sessionKey]; ok {
					s.claudeSID = event.SessionID
					s.lastActive = time.Now()
					slog.Info("feishu: session updated", "chat_id", chatID, "session_key", sessionKey, "claude_sid", event.SessionID)
				}
				cs.mu.Unlock()
			}
		case "thinking":
			state.SetThinking(event.Text)
		case "result":
			state.SetFinal(event.Text)
			cs.mu.Lock()
			if s, ok := cs.sessions[sessionKey]; ok {
				s.lastActive = time.Now()
			}
			cs.mu.Unlock()
		}
		return streamCtrl.UpdateCard(ctx, state.BuildCardWithName(sessionName))
	})

	streamCtrl.Close(ctx)

	if err != nil {
		slog.Error("feishu: claude stream failed", "chat_id", chatID, "session_key", sessionKey, "error", err)
		return
	}

	// Send extra cards if content was split
	for _, cardJSON := range state.BuildExtraCardsWithName(sessionName) {
		if _, sendErr := f.ch.Send(ctx, &types.SendInput{ChatID: chatID, Card: cardJSON}); sendErr != nil {
			slog.Error("feishu: failed to send extra card", "chat_id", chatID, "error", sendErr)
		}
	}

	slog.Info("feishu: message processed", "chat_id", chatID, "session_key", sessionKey)
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

	// Register card action handler for session and task management
	ch.OnCardAction(func(ctx context.Context, event *types.CardActionEvent) error {
		return f.handleCardAction(ctx, event)
	})
	slog.Info("feishu card action handler registered")

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
// Commands are handled synchronously. Non-command messages are processed
// concurrently in their own goroutines.
func (f *FeishuChannel) HandleMessage(ctx context.Context, ch types.Channel, msg *types.NormalizedMessage) error {
	slog.Info("received feishu message",
		"chat_id", msg.ChatID,
		"chat_type", msg.ChatType,
		"user_id", msg.UserID,
		"content", msg.Content,
		"message_id", msg.MessageID,
	)

	// 命令分发：去掉 mention 前缀后检测命令（群聊中 @机器人 会产生 @_user_1 等占位符）
	trimmed := StripMentionPrefix(msg.Content)
	for prefix, handler := range f.commands {
		if trimmed == prefix || strings.HasPrefix(trimmed, prefix+" ") {
			return handler(ctx, ch, msg)
		}
	}

	// 非命令消息：获取活跃会话，在独立 goroutine 中处理
	// 同一会话的消息串行执行（保护 claudeSID 链），不同会话并发
	activeSession := f.getOrCreateActiveSession(msg.ChatID)
	sessionKey := activeSession.key
	sem := activeSession.sem

	// Use Background context because this goroutine outlives the message handler.
	// The 35-minute timeout prevents runaway Claude sessions.
	msgCtx, cancel := context.WithTimeout(context.Background(), 35*time.Minute)
	go func() {
		defer cancel()
		sem <- struct{}{} // 同一会话排队等待
		defer func() { <-sem }()
		f.processMessage(msgCtx, ch, msg, sessionKey)
	}()

	slog.Info("feishu: message dispatched", "chat_id", msg.ChatID, "session_key", sessionKey, "session_name", activeSession.name)
	return nil
}

// handleCardAction processes button clicks on interactive cards.
func (f *FeishuChannel) handleCardAction(ctx context.Context, event *types.CardActionEvent) error {
	action, _ := event.Action.Value["action"].(string)
	taskID, _ := event.Action.Value["task_id"].(string)
	sessionKey, _ := event.Action.Value["session_key"].(string)

	slog.Info("feishu: card action received",
		"action", action,
		"task_id", taskID,
		"session_key", sessionKey,
		"chat_id", event.ChatID,
		"operator", event.Operator.OpenID,
		"form_value", fmt.Sprintf("%v", event.Action.FormValue),
	)

	if action == "" {
		return nil
	}

	// Handle session actions
	switch action {
	case "switch_session":
		if sessionKey == "" {
			return nil
		}
		if err := f.SwitchSession(event.ChatID, sessionKey); err != nil {
			f.ch.Send(ctx, &types.SendInput{ChatID: event.ChatID, Text: fmt.Sprintf("❌ 切换失败: %v", err)})
		} else {
			sessions, activeKey := f.GetSessions(event.ChatID)
			for _, s := range sessions {
				if s.key == activeKey {
					f.ch.Send(ctx, &types.SendInput{ChatID: event.ChatID, Text: fmt.Sprintf("✅ 已切换到会话: %s", s.name)})
					break
				}
			}
		}
		return nil
	case "close_session":
		if sessionKey == "" {
			return nil
		}
		if err := f.CloseSession(event.ChatID, sessionKey); err != nil {
			f.ch.Send(ctx, &types.SendInput{ChatID: event.ChatID, Text: fmt.Sprintf("❌ 关闭失败: %v", err)})
		} else {
			f.ch.Send(ctx, &types.SendInput{ChatID: event.ChatID, Text: "✅ 会话已关闭"})
		}
		return nil
	}

	// Handle create_task form submission (no taskID required)
	if action == "create_task" {
		return f.handleCreateTaskAction(ctx, event)
	}

	// Handle task actions (requires scheduler)
	if f.scheduler == nil || taskID == "" {
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

// StripMentionPrefix removes leading @_user_N mention placeholders from message content.
// In Feishu group chats, @mentioning the bot produces content like "@_user_1 /new".
func StripMentionPrefix(content string) string {
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
	ctx, cancel := context.WithTimeout(context.Background(), 35*time.Minute)
	defer cancel()

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

	// Send extra cards if content was split
	for _, cardJSON := range state.BuildExtraCards() {
		if _, sendErr := f.ch.Send(ctx, &types.SendInput{ChatID: chatID, Card: cardJSON}); sendErr != nil {
			slog.Error("feishu: failed to send extra card", "chat_id", chatID, "error", sendErr)
		}
	}

	slog.Info("feishu: create task stream completed", "chat_id", chatID)
}
