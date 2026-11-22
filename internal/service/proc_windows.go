//go:build windows

package service

import (
	"os/exec"
	"syscall"
)

const createNoWindowFlag = 0x08000000

// HideChildWindow configures a child process on Windows so it runs without flashing a console window.
func HideChildWindow(cmd *exec.Cmd) {
	if cmd == nil {
		return
	}
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.HideWindow = true
	cmd.SysProcAttr.CreationFlags |= createNoWindowFlag
}
