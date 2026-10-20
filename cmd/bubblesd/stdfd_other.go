//go:build !linux && !darwin

package main

import "errors"

// dupOnto 在不支持的平台上返回错误，redirectStderrTo 会跳过重定向（保持原行为）。
func dupOnto(oldfd, newfd int) error {
	return errors.New("stderr redirect is not supported on this platform")
}
