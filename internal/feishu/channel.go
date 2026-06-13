package feishu

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"time"

	lark "github.com/larksuite/oapi-sdk-go/v3"
	larkcard "github.com/larksuite/oapi-sdk-go/v3/card"
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
// Instead of Stream+Append (which hits Feishu's message edit limit),
// we use Send() for each logical segment — each becomes a separate message.
// No edit limit, and the conversation reads naturally.
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

	// Each segment is sent as a standalone message via Send(), no Append/Edit involved.
	var segmentCount int
	var totalBytes int

	err := agent.ClaudeStreamWithTimeout(cfg.ClaudePath, msg.Content, cfg.WorkDir, func(chunk string) error {
		segmentCount++
		totalBytes += len(chunk)

		input := &types.SendInput{
			ChatID:   msg.ChatID,
			Markdown: chunk,
		}
		// Only the first segment is a "reply" to the original message;
		// subsequent segments are standalone messages in the chat.
		if segmentCount == 1 {
			input.ReplyMessageID = msg.MessageID
		}

		slog.Info("feishu: sending message segment",
			"chat_id", msg.ChatID,
			"segment#", segmentCount,
			"bytes", len(chunk),
			"is_reply", segmentCount == 1,
		)

		_, sendErr := ch.Send(ctx, input)
		if sendErr != nil {
			slog.Error("feishu: failed to send message segment",
				"chat_id", msg.ChatID,
				"segment#", segmentCount,
				"bytes", len(chunk),
				"error", sendErr,
			)
			return sendErr
		}

		slog.Info("feishu: message segment sent",
			"chat_id", msg.ChatID,
			"segment#", segmentCount,
			"bytes", len(chunk),
		)
		return nil
	})

	if err != nil {
		slog.Error("feishu: claude stream failed",
			"chat_id", msg.ChatID,
			"error", err,
			"segments_sent", segmentCount,
			"total_bytes", totalBytes,
		)
		return err
	}

	slog.Info("feishu: all segments completed",
		"chat_id", msg.ChatID,
		"total_segments", segmentCount,
		"total_bytes", totalBytes,
	)
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

// BuildTaskCardJSON builds a Feishu interactive card from a list of task summaries.
// Each task row has action buttons appropriate to its type (cron vs one-time).
func BuildTaskCardJSON(summaries []daemon.TaskSummary) (string, error) {
	if len(summaries) == 0 {
		card := larkcard.NewMessageCard().
			Header(larkcard.NewMessageCardHeader().
				Template(larkcard.TemplateBlue).
				Title(larkcard.NewMessageCardPlainText().Content("📋 任务列表"))).
			Elements([]larkcard.MessageCardElement{
				larkcard.NewMessageCardDiv().
					Text(larkcard.NewMessageCardPlainText().Content("当前没有活跃任务")),
			})
		return card.Build().String()
	}

	var elements []larkcard.MessageCardElement

	var cronTasks, oneTimeTasks []daemon.TaskSummary
	for _, t := range summaries {
		if t.Schedule != "" {
			cronTasks = append(cronTasks, t)
		} else {
			oneTimeTasks = append(oneTimeTasks, t)
		}
	}

	if len(cronTasks) > 0 {
		elements = append(elements,
			larkcard.NewMessageCardDiv().
				Text(larkcard.NewMessageCardPlainText().
					Content("⏰ 定时任务")),
		)
		for _, t := range cronTasks {
			nextRun := t.NextRunAt
			if nextRun == "" {
				nextRun = "-"
			}
			row := fmt.Sprintf("**%s**  \nCron: `%s`  |  下次执行: %s  |  状态: %s",
				t.Name, t.Schedule, nextRun, t.Status)
			if t.Prompt != "" {
				desc := t.Prompt
				if len([]rune(desc)) > 100 {
					desc = string([]rune(desc)[:100]) + "…"
				}
				row += fmt.Sprintf("  \n> %s", desc)
			}

			var btns []larkcard.MessageCardActionElement
			if t.Status == "paused" {
				// Paused cron task: show resume + delete
				resumeBtn := larkcard.NewMessageCardEmbedButton().
					Text(larkcard.NewMessageCardPlainText().Content("▶ 恢复")).
					Type(larkcard.MessageCardButtonTypePrimary).
					Value(map[string]interface{}{"action": "resume", "task_id": t.ID})
				deleteBtn := larkcard.NewMessageCardEmbedButton().
					Text(larkcard.NewMessageCardPlainText().Content("🗑 删除")).
					Type(larkcard.MessageCardButtonTypeDanger).
					Value(map[string]interface{}{"action": "delete", "task_id": t.ID})
				btns = []larkcard.MessageCardActionElement{resumeBtn, deleteBtn}
			} else {
				// Active cron task: show pause + delete
				pauseBtn := larkcard.NewMessageCardEmbedButton().
					Text(larkcard.NewMessageCardPlainText().Content("⏸ 暂停")).
					Type(larkcard.MessageCardButtonTypeDanger).
					Value(map[string]interface{}{"action": "pause", "task_id": t.ID})
				deleteBtn := larkcard.NewMessageCardEmbedButton().
					Text(larkcard.NewMessageCardPlainText().Content("🗑 删除")).
					Type(larkcard.MessageCardButtonTypeDanger).
					Value(map[string]interface{}{"action": "delete", "task_id": t.ID})
				btns = []larkcard.MessageCardActionElement{pauseBtn, deleteBtn}
			}

			elements = append(elements,
				larkcard.NewMessageCardDiv().
					Text(larkcard.NewMessageCardLarkMd().Content(row)),
				larkcard.NewMessageCardAction().
					Actions(btns),
			)
		}
	}

	if len(oneTimeTasks) > 0 {
		elements = append(elements,
			larkcard.NewMessageCardDiv().
				Text(larkcard.NewMessageCardPlainText().
					Content("📌 一次性任务")),
		)
		for _, t := range oneTimeTasks {
			runAt := t.RunAt
			if runAt == "" {
				runAt = "-"
			}
			row := fmt.Sprintf("**%s**  \n计划时间: %s  |  状态: %s",
				t.Name, runAt, t.Status)
			if t.Prompt != "" {
				desc := t.Prompt
				if len([]rune(desc)) > 100 {
					desc = string([]rune(desc)[:100]) + "…"
				}
				row += fmt.Sprintf("  \n> %s", desc)
			}

			deleteBtn := larkcard.NewMessageCardEmbedButton().
				Text(larkcard.NewMessageCardPlainText().Content("🗑 删除")).
				Type(larkcard.MessageCardButtonTypeDanger).
				Value(map[string]interface{}{"action": "delete", "task_id": t.ID})

			elements = append(elements,
				larkcard.NewMessageCardDiv().
					Text(larkcard.NewMessageCardLarkMd().Content(row)),
				larkcard.NewMessageCardAction().
					Actions([]larkcard.MessageCardActionElement{deleteBtn}),
			)
		}
	}

	card := larkcard.NewMessageCard().
		Header(larkcard.NewMessageCardHeader().
			Template(larkcard.TemplateBlue).
			Title(larkcard.NewMessageCardPlainText().Content(fmt.Sprintf("📋 任务列表 (%d)", len(summaries))))).
		Elements(elements)

	raw, err := card.Build().String()
	if err != nil {
		return "", err
	}
	return raw, nil
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
