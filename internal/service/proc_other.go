//go:build !windows

package service

import "os/exec"

// HideChildWindow is a no-op on non-Windows platforms.
func HideChildWindow(cmd *exec.Cmd) {}
