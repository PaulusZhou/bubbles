package main

import (
	"os"
	"syscall"
)

// redirectStderrTo 把 fd 2 重定向到 target（通常是日志文件），并返回原始 stderr
// 的副本（独立 fd ≥ 3）。
//
// 背景：Go 运行时的 panic / fatal error 只会写 fd 2，而 os.File 写 fd 1/2 遇到
// EPIPE/EIO 会直接触发 SIGPIPE 杀死进程。此前 daemon 以 `nohup ... > /dev/null 2>&1`
// 启动时崩溃栈被丢弃、以 `bubbles daemon start` 继承终端启动时终端断线会被 SIGPIPE
// 带走——进程“无声死亡”，日志里连一条记录都没有，无法排查。
//
// 把 fd 2 换成日志文件后：崩溃栈一定进日志；原始 stderr 的副本是普通 fd，终端或
// journald 管道断开时写失败只返回错误，不会再连累进程本身。
func redirectStderrTo(target *os.File) *os.File {
	saved, err := syscall.Dup(int(os.Stderr.Fd()))
	if err != nil {
		return nil
	}
	if err := dupOnto(int(target.Fd()), int(os.Stderr.Fd())); err != nil {
		syscall.Close(saved)
		return nil
	}
	return os.NewFile(uintptr(saved), "console-stderr")
}
