//go:build linux

package main

import "syscall"

// dupOnto 把 oldfd 复制到 newfd（覆盖其原指向）。
// linux/arm64 等架构的 syscall 包不提供 Dup2，统一走 dup3。
func dupOnto(oldfd, newfd int) error {
	return syscall.Dup3(oldfd, newfd, 0)
}
