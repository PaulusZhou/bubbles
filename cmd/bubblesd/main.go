package main

import (
	"log/slog"
	"os"

	"github.com/pauluszhou/bubbles/internal/daemon"
	"github.com/pauluszhou/bubbles/internal/store"
)

func main() {
	// 设置日志
	slog.SetDefault(slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{
		Level: slog.LevelInfo,
	})))

	// 打开数据库
	s, err := store.New(daemon.DBPath())
	if err != nil {
		slog.Error("failed to open database", "error", err)
		os.Exit(1)
	}
	defer s.Close()

	// 创建调度器并运行（阻塞）
	scheduler := daemon.NewScheduler(s)
	if err := scheduler.Run(); err != nil {
		slog.Error("scheduler failed", "error", err)
		os.Exit(1)
	}
}
