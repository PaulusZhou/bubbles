//go:build darwin

package main

import "syscall"

// dupOnto 把 oldfd 复制到 newfd（覆盖其原指向）。darwin 无 dup3，使用 dup2。
func dupOnto(oldfd, newfd int) error {
	return syscall.Dup2(oldfd, newfd)
}
