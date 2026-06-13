package main

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"time"

	"github.com/larksuite/oapi-sdk-go/v3/channel/types"

	"github.com/pauluszhou/bubbles/internal/config"
	"github.com/pauluszhou/bubbles/internal/daemon"
	"github.com/pauluszhou/bubbles/internal/feishu"
	"github.com/pauluszhou/bubbles/internal/store"
)

func main() {
	// 加载配置
	cfg := config.Load()
	slog.Info("bubblesd starting", "data_dir", cfg.DataDir, "feishu_app_id", cfg.FeishuAppID, "claude_path", cfg.ClaudePath)

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
	scheduler := daemon.NewScheduler(s)

	// 如果配置了飞书，启动飞书 Channel
	if cfg.FeishuAppID != "" {
		slog.Info("feishu configuration detected, initializing channel", "app_id", cfg.FeishuAppID)
		fch := feishu.New(cfg)
		if fch != nil {
			// 注册 /cron 命令：查询活跃任务列表
			fch.RegisterCommand("/cron", func(ctx context.Context, ch types.Channel, msg *types.NormalizedMessage) error {
				md, err := scheduler.FormatActiveTaskList()
				if err != nil {
					slog.Error("feishu: /cron failed", "error", err)
					md = fmt.Sprintf("❌ 查询任务失败: %v", err)
				}
				_, sendErr := ch.Send(ctx, &types.SendInput{
					ChatID:         msg.ChatID,
					Markdown:       md,
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
