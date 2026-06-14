package main

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/larksuite/oapi-sdk-go/v3/channel/types"

	"github.com/pauluszhou/bubbles/internal/config"
	"github.com/pauluszhou/bubbles/internal/daemon"
	"github.com/pauluszhou/bubbles/internal/feishu"
	"github.com/pauluszhou/bubbles/internal/store"
)

func main() {
	// 加载配置
	cfg, err := config.Load()
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
	slog.Info("bubblesd starting", "data_dir", cfg.DataDir, "work_dir", cfg.WorkDir, "feishu_app_id", cfg.FeishuAppID, "claude_path", cfg.ClaudePath)

	// 确保数据目录存在
	if err := os.MkdirAll(cfg.DataDir, 0755); err != nil {
		slog.Error("failed to create data dir", "dir", cfg.DataDir, "error", err)
		os.Exit(1)
	}

	// 设置日志：同时输出到 stderr 和日志文件
	logPath := filepath.Join(cfg.DataDir, "bubblesd.log")
	logFile, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
	if err != nil {
		slog.Error("failed to open log file", "path", logPath, "error", err)
		os.Exit(1)
	}
	defer logFile.Close()

	multiWriter := io.MultiWriter(os.Stderr, logFile)
	slog.SetDefault(slog.New(slog.NewTextHandler(multiWriter, &slog.HandlerOptions{
		Level: slog.LevelInfo,
	})))

	slog.Info("bubblesd log initialized", "log_file", logPath)

	// 打开数据库
	dbStart := time.Now()
	s, err := store.New(cfg.DBPath())
	if err != nil {
		slog.Error("failed to open database", "path", cfg.DBPath(), "error", err)
		os.Exit(1)
	}
	defer s.Close()
	slog.Info("database opened", "path", cfg.DBPath(), "duration", time.Since(dbStart))

	// 创建调度器
	scheduler := daemon.NewScheduler(s, cfg)

	// 如果配置了飞书，启动飞书 Channel
	if cfg.FeishuAppID != "" {
		slog.Info("feishu configuration detected, initializing channel", "app_id", cfg.FeishuAppID)
		fch := feishu.New(cfg)
		if fch != nil {
			fch.SetScheduler(scheduler)

			// 注册 /cron 命令：发送交互式任务卡片
			fch.RegisterCommand("/cron", func(ctx context.Context, ch types.Channel, msg *types.NormalizedMessage) error {
				summaries, err := scheduler.GetAllTaskSummary()
				if err != nil {
					slog.Error("feishu: /cron failed", "error", err)
					_, sendErr := ch.Send(ctx, &types.SendInput{
						ChatID:         msg.ChatID,
						Markdown:       fmt.Sprintf("❌ 查询任务失败: %v", err),
						ReplyMessageID: msg.MessageID,
					})
					return sendErr
				}
				cardJSON, err := feishu.BuildTaskCardJSON(summaries)
				if err != nil {
					slog.Error("feishu: /cron card build failed", "error", err)
					_, sendErr := ch.Send(ctx, &types.SendInput{
						ChatID:         msg.ChatID,
						Markdown:       "❌ 生成卡片失败",
						ReplyMessageID: msg.MessageID,
					})
					return sendErr
				}
				_, sendErr := ch.Send(ctx, &types.SendInput{
					ChatID:         msg.ChatID,
					Card:           cardJSON,
					ReplyMessageID: msg.MessageID,
				})
				return sendErr
			})

			// 注册 /new 命令：创建新会话并切换为活跃会话
			fch.RegisterCommand("/new", func(ctx context.Context, ch types.Channel, msg *types.NormalizedMessage) error {
				trimmed := feishu.StripMentionPrefix(msg.Content)
				name := strings.TrimSpace(strings.TrimPrefix(trimmed, "/new"))
				s := fch.NewSession(msg.ChatID, name)
				_, sendErr := ch.Send(ctx, &types.SendInput{
					ChatID:         msg.ChatID,
					Markdown:       fmt.Sprintf("✅ 已创建并切换到会话: **%s**", s.Name()),
					ReplyMessageID: msg.MessageID,
				})
				return sendErr
			})

			// 注册 /sessions 命令：显示所有会话卡片
			fch.RegisterCommand("/sessions", func(ctx context.Context, ch types.Channel, msg *types.NormalizedMessage) error {
				sessions, activeKey := fch.GetSessions(msg.ChatID)
				cardJSON := feishu.BuildSessionsCard(sessions, activeKey)
				_, sendErr := ch.Send(ctx, &types.SendInput{
					ChatID:         msg.ChatID,
					Card:           cardJSON,
					ReplyMessageID: msg.MessageID,
				})
				return sendErr
			})

			// 注册 /stop 命令：停止当前活跃会话的 Claude 流，保留会话 ID 以便下次继续
			fch.RegisterCommand("/stop", func(ctx context.Context, ch types.Channel, msg *types.NormalizedMessage) error {
				err := fch.StopActiveSession(msg.ChatID)
				var reply string
				if err != nil {
					reply = fmt.Sprintf("⚠️ %v", err)
				} else {
					reply = "⏹ 已停止当前任务。下次发消息将自动继续会话。"
				}
				_, sendErr := ch.Send(ctx, &types.SendInput{
					ChatID:         msg.ChatID,
					Markdown:       reply,
					ReplyMessageID: msg.MessageID,
				})
				return sendErr
			})

			// 注册 /cron-new 命令：发送创建任务的表单卡片
			fch.RegisterCommand("/cron-new", func(ctx context.Context, ch types.Channel, msg *types.NormalizedMessage) error {
				cardJSON := feishu.BuildNewTaskCardJSON()
				_, sendErr := ch.Send(ctx, &types.SendInput{
					ChatID:         msg.ChatID,
					Card:           cardJSON,
					ReplyMessageID: msg.MessageID,
				})
				return sendErr
			})
			feishuStart := time.Now()
			if err := fch.Start(context.Background()); err != nil {
				slog.Error("failed to start feishu channel",
					"app_id", cfg.FeishuAppID,
					"duration", time.Since(feishuStart),
					"error", err,
				)
			} else {
				slog.Info("feishu channel started",
					"app_id", cfg.FeishuAppID,
					"duration", time.Since(feishuStart),
				)
				scheduler.SetFeishuStopper(fch)
				scheduler.SetFeishuNotifier(fch)
			}
		} else {
			slog.Warn("feishu channel creation failed despite app_id being set", "app_id", cfg.FeishuAppID)
		}
	} else {
		slog.Info("feishu channel disabled (feishu_app_id not configured)")
	}

	// 创建调度器并运行（阻塞）
	slog.Info("bubblesd entering main loop")
	if err := scheduler.Run(); err != nil {
		slog.Error("scheduler exited with error", "error", err)
		os.Exit(1)
	}
	slog.Info("bubblesd exited cleanly")
}
