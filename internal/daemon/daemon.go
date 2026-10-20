package daemon

import (
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strconv"
	"syscall"
	"time"

	"github.com/pauluszhou/bubbles/internal/config"
)

const (
	DefaultSocketName = "bubblesd.sock"
	DefaultPIDName    = "bubblesd.pid"
)

// DefaultDataDir returns the default data directory for bubbles.
// Deprecated: Use config.DefaultDataDir() instead.
func DefaultDataDir() string {
	return config.DefaultDataDir()
}

func SocketPath() string {
	return filepath.Join(DefaultDataDir(), DefaultSocketName)
}

func PIDPath() string {
	return filepath.Join(DefaultDataDir(), DefaultPIDName)
}

// IsRunning checks if the daemon is running by reading the PID file and signaling the process.
func IsRunning() (bool, int) {
	pidFile := PIDPath()
	data, err := os.ReadFile(pidFile)
	if err != nil {
		return false, 0
	}
	pid, err := strconv.Atoi(string(data))
	if err != nil {
		return false, 0
	}
	// 检查进程是否存在
	process, err := os.FindProcess(pid)
	if err != nil {
		return false, 0
	}
	if err := process.Signal(syscall.Signal(0)); err != nil {
		// 进程不存在，清理 PID 文件
		os.Remove(pidFile)
		return false, 0
	}
	return true, pid
}

// Start forks the daemon into the background.
func Start() error {
	if running, pid := IsRunning(); running {
		return fmt.Errorf("daemon is already running (PID: %d)", pid)
	}

	dataDir := DefaultDataDir()
	if err := os.MkdirAll(dataDir, 0755); err != nil {
		return fmt.Errorf("create data dir: %w", err)
	}

	// 获取当前可执行文件路径
	exe, err := os.Executable()
	if err != nil {
		return fmt.Errorf("get executable path: %w", err)
	}

	// 找到 bubblesd 可执行文件（与 bubbles 同目录）
	bubblesdPath := filepath.Join(filepath.Dir(exe), "bubblesd")
	if _, err := os.Stat(bubblesdPath); os.IsNotExist(err) {
		return fmt.Errorf("bubblesd not found at %s, run 'make build' first", bubblesdPath)
	}

	// 写 PID 文件（使用当前进程 PID，fork 后子进程会覆盖）
	pidFile := PIDPath()

	// stdio：stdin 接 /dev/null，stdout/stderr 接日志文件，不继承 CLI 的终端。
	// 终端/SSH 断线后写已失效的 pty 会触发 SIGPIPE/EIO，会把守护进程一起带走。
	devNull, err := os.OpenFile(os.DevNull, os.O_RDWR, 0)
	if err != nil {
		return fmt.Errorf("open %s: %w", os.DevNull, err)
	}
	defer devNull.Close()
	logFile, err := os.OpenFile(filepath.Join(DefaultDataDir(), "bubblesd.log"), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
	if err != nil {
		return fmt.Errorf("open daemon log: %w", err)
	}
	defer logFile.Close()

	// Fork 后台进程。Setsid：子进程自成会话、不再受启动终端控制，终端关闭时
	// 不会收到 SIGHUP（配合 daemon 对 SIGHUP 的忽略，双保险）。
	attr := &syscall.ProcAttr{
		Dir: dataDir,
		Env: os.Environ(),
		Files: []uintptr{
			devNull.Fd(),
			logFile.Fd(),
			logFile.Fd(),
		},
		Sys: &syscall.SysProcAttr{Setsid: true},
	}

	pid, err := syscall.ForkExec(bubblesdPath, []string{"bubblesd"}, attr)
	if err != nil {
		return fmt.Errorf("fork daemon: %w", err)
	}

	// 等待一小段时间确认启动
	time.Sleep(500 * time.Millisecond)

	// 检查子进程是否仍在运行
	process, err := os.FindProcess(pid)
	if err != nil {
		return fmt.Errorf("find daemon process: %w", err)
	}
	if err := process.Signal(syscall.Signal(0)); err != nil {
		return fmt.Errorf("daemon failed to start")
	}

	// 写 PID 文件
	if err := os.WriteFile(pidFile, []byte(strconv.Itoa(pid)), 0644); err != nil {
		slog.Warn("failed to write pid file", "error", err)
	}

	slog.Info("daemon started", "pid", pid)
	return nil
}

// Stop sends SIGTERM to the daemon process.
func Stop() error {
	running, pid := IsRunning()
	if !running {
		return fmt.Errorf("daemon is not running")
	}

	process, err := os.FindProcess(pid)
	if err != nil {
		return fmt.Errorf("find daemon process: %w", err)
	}

	if err := process.Signal(syscall.SIGTERM); err != nil {
		return fmt.Errorf("send SIGTERM: %w", err)
	}

	// 等待进程退出
	for i := 0; i < 30; i++ {
		time.Sleep(100 * time.Millisecond)
		if err := process.Signal(syscall.Signal(0)); err != nil {
			// 进程已退出
			os.Remove(PIDPath())
			os.Remove(SocketPath())
			slog.Info("daemon stopped")
			return nil
		}
	}

	// 强制杀死
	process.Signal(syscall.SIGKILL)
	os.Remove(PIDPath())
	os.Remove(SocketPath())
	slog.Info("daemon killed")
	return nil
}
