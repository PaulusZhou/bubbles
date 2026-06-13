package feishu

import (
	"context"
	"encoding/json"
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

// CommandHandler handles a Feishu slash command.
type CommandHandler func(ctx context.Context, ch types.Channel, msg *types.NormalizedMessage) error

// FeishuChannel wraps the SDK Channel and manages the Feishu bot lifecycle.
type FeishuChannel struct {
	ch              types.Channel
	cfg             *config.Config
	commands        map[string]CommandHandler // prefix -> handler
	defaultChatID    string
	scheduler       *daemon.Scheduler // for direct task operations on card callbacks
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
		ch:             ch,
		cfg:            cfg,
		commands:       make(map[string]CommandHandler),
		defaultChatID:   cfg.FeishuChatID,
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

// NotifyTaskCompletion sends a task completion notification to Feishu.
// Implements daemon.FeishuNotifier.
func (f *FeishuChannel) NotifyTaskCompletion(ctx context.Context, completion daemon.TaskCompletion) error {
	if f.defaultChatID == "" {
		slog.Debug("feishu: no default chat_id configured, skipping notification",
			"task_id", completion.TaskID,
			"task_name", completion.TaskName,
		)
		return nil
	}

	taskName := completion.TaskName
	if taskName == "" {
		taskName = completion.TaskID
	}

	statusEmoji := "✅"
	statusText := "成功"
	if completion.Status == "failed" {
		statusEmoji = "❌"
		statusText = "失败"
	}

	duration := completion.Duration.Round(time.Second)
	startTime := completion.StartedAt.Format("15:04:05")
	endTime := completion.EndedAt.Format("15:04:05")

	header := fmt.Sprintf("%s **任务完成: %s**\n📋 Task ID: `%s`\n📊 状态: %s %s\n⏱️ 耗时: %s\n🕐 开始: %s | 结束: %s\n",
		statusEmoji,
		taskName,
		completion.TaskID,
		statusEmoji,
		statusText,
		duration,
		startTime,
		endTime,
	)

	// Truncate output if too long for a single message
	output := completion.Output
	maxOutput := 3000
	if len(output) > maxOutput {
		output = output[:maxOutput] + "\n\n... (输出被截断)"
	}

	// Wrap in a code block for readability
	content := header + "\n```\n" + output + "\n```"

	chunks := splitMessage(content, 3500)
	for i, chunk := range chunks {
		_, err := f.ch.Send(ctx, &types.SendInput{
			ChatID: f.defaultChatID,
			Markdown: chunk,
		})
		if err != nil {
			slog.Error("feishu: failed to send task completion notification",
				"task_id", completion.TaskID,
				"chunk", fmt.Sprintf("%d/%d", i+1, len(chunks)),
				"error", err,
			)
			return err
		}
		slog.Info("feishu: task completion notification sent",
			"task_id", completion.TaskID,
			"chunk", fmt.Sprintf("%d/%d", i+1, len(chunks)),
		)
	}
	return nil
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
		return HandleMessage(ctx, ch, msg, f.cfg, f.commands)
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
// in a single card that updates in place.
func HandleMessage(ctx context.Context, ch types.Channel, msg *types.NormalizedMessage, cfg *config.Config, commands map[string]CommandHandler) error {
	slog.Info("received feishu message",
		"chat_id", msg.ChatID,
		"chat_type", msg.ChatType,
		"user_id", msg.UserID,
		"content", msg.Content,
		"message_id", msg.MessageID,
	)

	// 命令分发：检测注册的命令前缀
	trimmed := strings.TrimSpace(msg.Content)
	for prefix, handler := range commands {
		if trimmed == prefix || strings.HasPrefix(trimmed, prefix+" ") {
			return handler(ctx, ch, msg)
		}
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

	err = agent.ClaudeStreamWithEventsTimeout(cfg.ClaudePath, msg.Content, cfg.WorkDir, func(event agent.StreamEvent) error {
		slog.Debug("feishu: stream event", "type", event.Type, "text_len", len(event.Text), "text_preview", func() string {
			if len(event.Text) > 150 {
				return event.Text[:150]
			}
			return event.Text
		}())
		switch event.Type {
		case "thinking":
			state.AppendThinking(event.Text)
		case "result":
			state.SetFinal(event.Text)
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
	)

	if taskID == "" || action == "" {
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


// taskButtonRow creates a v2 card column_set with action buttons.
// v2 cards don't support the "action" tag; buttons must be inside column_set -> column.
func taskButtonRow(buttons ...map[string]interface{}) map[string]interface{} {
	columns := make([]map[string]interface{}, len(buttons))
	for i, btn := range buttons {
		columns[i] = map[string]interface{}{
			"tag":    "column",
			"width":  "auto",
			"elements": []map[string]interface{}{btn},
		}
	}
	return map[string]interface{}{
		"tag":     "column_set",
		"columns": columns,
	}
}

// taskButton creates a v2 card button with value for SDK callback compatibility.
func taskButton(text, buttonType, action, taskID string) map[string]interface{} {
	value := map[string]string{"action": action, "task_id": taskID}
	return map[string]interface{}{
		"tag":  "button",
		"text": map[string]string{"tag": "plain_text", "content": text},
		"type": buttonType,
		"value":     value,
		"behaviors": []map[string]interface{}{{"type": "callback", "value": value}},
	}
}

// BuildTaskCardJSON builds a Feishu v2 interactive card from a list of task summaries.
// Each task row has action buttons appropriate to its type (cron vs one-time).
func BuildTaskCardJSON(summaries []daemon.TaskSummary) (string, error) {
	if len(summaries) == 0 {
		card := map[string]interface{}{
			"schema": "2.0",
			"header": map[string]interface{}{
				"template": "blue",
				"title":    map[string]interface{}{"tag": "plain_text", "content": "📋 任务列表"},
			},
			"body": map[string]interface{}{
				"elements": []map[string]interface{}{
					{"tag": "markdown", "content": "当前没有活跃任务"},
				},
			},
		}
		bs, err := json.Marshal(card)
		return string(bs), err
	}

	var cronTasks, oneTimeTasks []daemon.TaskSummary
	for _, t := range summaries {
		if t.Schedule != "" {
			cronTasks = append(cronTasks, t)
		} else {
			oneTimeTasks = append(oneTimeTasks, t)
		}
	}

	var elements []map[string]interface{}

	if len(cronTasks) > 0 {
		elements = append(elements, map[string]interface{}{
			"tag": "markdown", "content": "⏰ **定时任务**",
		})
		for _, t := range cronTasks {
			nextRun := t.NextRunAt
			if nextRun == "" {
				nextRun = "-"
			}
			row := fmt.Sprintf("**%s**\nCron: `%s` | 下次执行: %s | 状态: %s",
				t.Name, t.Schedule, nextRun, t.Status)
			if t.Prompt != "" {
				desc := t.Prompt
				if len([]rune(desc)) > 100 {
					desc = string([]rune(desc)[:100]) + "…"
				}
				row += fmt.Sprintf("\n> %s", desc)
			}
			elements = append(elements, map[string]interface{}{"tag": "markdown", "content": row})

			if t.Status == "paused" {
				elements = append(elements, taskButtonRow(
					taskButton("▶ 恢复", "primary", "resume", t.ID),
					taskButton("🗑 删除", "danger", "delete", t.ID),
				))
			} else {
				elements = append(elements, taskButtonRow(
					taskButton("⏸ 暂停", "danger", "pause", t.ID),
					taskButton("🗑 删除", "danger", "delete", t.ID),
				))
			}
		}
	}

	if len(oneTimeTasks) > 0 {
		elements = append(elements, map[string]interface{}{
			"tag": "markdown", "content": "📌 **一次性任务**",
		})
		for _, t := range oneTimeTasks {
			runAt := t.RunAt
			if runAt == "" {
				runAt = "-"
			}
			row := fmt.Sprintf("**%s**\n计划时间: %s | 状态: %s", t.Name, runAt, t.Status)
			if t.Prompt != "" {
				desc := t.Prompt
				if len([]rune(desc)) > 100 {
					desc = string([]rune(desc)[:100]) + "…"
				}
				row += fmt.Sprintf("\n> %s", desc)
			}
			elements = append(elements, map[string]interface{}{"tag": "markdown", "content": row})
			elements = append(elements, taskButtonRow(
				taskButton("🗑 删除", "danger", "delete", t.ID),
			))
		}
	}

	card := map[string]interface{}{
		"schema": "2.0",
		"header": map[string]interface{}{
			"template": "blue",
			"title":    map[string]interface{}{"tag": "plain_text", "content": fmt.Sprintf("📋 任务列表 (%d)", len(summaries))},
		},
		"body": map[string]interface{}{
			"elements": elements,
		},
	}

	bs, err := json.Marshal(card)
	if err != nil {
		return "", err
	}
	return string(bs), nil
}

// cardState accumulates thinking and final text for a card stream.
type cardState struct {
	mu        sync.Mutex
	thinking  strings.Builder
	finalText strings.Builder
	gotResult bool
}

const cardThinkingMaxLen = 8000

func newCardState() *cardState {
	return &cardState{}
}

func (s *cardState) AppendThinking(text string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.thinking.WriteString(text)
}

func (s *cardState) SetFinal(text string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.gotResult = true
	s.finalText.WriteString(text)
}

// thinkingTruncated returns truncated thinking content with ellipsis if too long.
func (s *cardState) thinkingTruncated() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	text := s.thinking.String()
	if len(text) <= cardThinkingMaxLen {
		return text
	}
	return text[:cardThinkingMaxLen] + "\n\n... (省略)"
}

func (s *cardState) finalContent() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.finalText.String()
}

func (s *cardState) BuildCard() string {
	s.mu.Lock()
	hasFinal := s.finalText.Len() > 0
	s.mu.Unlock()

	thinking := s.thinkingTruncated()
	final := s.finalContent()

	headerTitle := "🤖 Claude Code"
	if hasFinal {
		headerTitle = "✅ Claude Code"
	}

	// Build v2 card JSON with markdown tag for proper rendering
	elements := []map[string]interface{}{
		{
			"tag":     "markdown",
			"content": "💭 **思考过程**\n\n" + thinking,
		},
	}

	if hasFinal {
		elements = append(elements,
			map[string]interface{}{"tag": "hr"},
			map[string]interface{}{
				"tag":     "markdown",
				"content": "📝 **最终回复**\n\n" + final,
			},
		)
	}

	card := map[string]interface{}{
		"schema": "2.0",
		"header": map[string]interface{}{
			"template": "blue",
			"title": map[string]interface{}{
				"tag":     "plain_text",
				"content": headerTitle,
			},
		},
		"body": map[string]interface{}{
			"elements": elements,
		},
	}

	bs, _ := json.Marshal(card)
	result := string(bs)

	slog.Debug("feishu: card JSON", "card", result)
	if len(result) > 2000 {
		slog.Info("feishu: card JSON truncated", "preview", result[:2000])
	} else {
		slog.Info("feishu: card JSON", "card", result)
	}
	return result
}

// splitMessage splits a long string into chunks of at most maxLen characters,
// trying to break at newlines.
func splitMessage(s string, maxLen int) []string {
	if len(s) <= maxLen {
		return []string{s}
	}

	var chunks []string
	for len(s) > maxLen {
		// Try to find a newline near the limit
		breakIdx := strings.LastIndex(s[:maxLen], "\n")
		if breakIdx == -1 || breakIdx < maxLen/2 {
			breakIdx = maxLen
		}
		chunks = append(chunks, s[:breakIdx])
		s = s[breakIdx:]
		// Skip leading newline on next chunk
		s = strings.TrimPrefix(s, "\n")
	}
	if s != "" {
		chunks = append(chunks, s)
	}
	return chunks
}
