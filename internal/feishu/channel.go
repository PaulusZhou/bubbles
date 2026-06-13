package feishu

import (
	"context"
	"fmt"
	"log/slog"
	"strings"

	lark "github.com/larksuite/oapi-sdk-go/v3"
	"github.com/larksuite/oapi-sdk-go/v3/channel"
	"github.com/larksuite/oapi-sdk-go/v3/channel/types"
	larkcore "github.com/larksuite/oapi-sdk-go/v3/core"
	larkdispatcher "github.com/larksuite/oapi-sdk-go/v3/event/dispatcher"
	larkws "github.com/larksuite/oapi-sdk-go/v3/ws"

	"github.com/pauluszhou/bubbles/internal/agent"
	"github.com/pauluszhou/bubbles/internal/config"
)

// CommandHandler handles a Feishu slash command.
type CommandHandler func(ctx context.Context, ch types.Channel, msg *types.NormalizedMessage) error

// FeishuChannel wraps the SDK Channel and manages the Feishu bot lifecycle.
type FeishuChannel struct {
	ch       types.Channel
	cfg      *config.Config
	commands map[string]CommandHandler // prefix -> handler
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
		ch:       ch,
		cfg:      cfg,
		commands: make(map[string]CommandHandler),
	}
}

// RegisterCommand registers a handler for a command prefix (e.g. "/cron").
func (f *FeishuChannel) RegisterCommand(prefix string, handler CommandHandler) {
	f.commands[prefix] = handler
	slog.Info("feishu: command registered", "prefix", prefix)
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

	err := agent.ClaudeStreamWithTimeout(cfg.ClaudePath, msg.Content, "", func(chunk string) error {
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

	result := agent.ClaudeWithTimeout(msg.Content, "")
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
