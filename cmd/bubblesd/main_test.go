package main

import (
	"os"
	"path/filepath"
	"testing"
)

// TestRedirectStderrTo_CapturesStderr 验证 fd 2 被重定向到日志文件：
// Go 运行时的 panic / fatal error 只写 fd 2，之前被 `> /dev/null` 丢弃，
// 进程"无声死亡"无从排查。
func TestRedirectStderrTo_CapturesStderr(t *testing.T) {
	logPath := filepath.Join(t.TempDir(), "bubblesd.log")
	logFile, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
	if err != nil {
		t.Fatalf("open log file: %v", err)
	}
	defer logFile.Close()

	console := redirectStderrTo(logFile)
	if console == nil {
		t.Fatal("redirectStderrTo returned nil")
	}
	// 还原 fd 2，不影响测试进程后续输出
	defer func() { _ = dupOnto(int(console.Fd()), int(os.Stderr.Fd())) }()

	if console.Fd() == os.Stderr.Fd() {
		t.Fatal("console copy must be an independent fd (not fd 2)")
	}

	if _, err := os.Stderr.WriteString("panic-probe\n"); err != nil {
		t.Fatalf("write stderr: %v", err)
	}

	data, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("read log file: %v", err)
	}
	if string(data) != "panic-probe\n" {
		t.Fatalf("stderr did not land in log file, got %q", string(data))
	}
}
