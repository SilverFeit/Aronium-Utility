//go:build !windows

package main

import (
	"context"
	"os/signal"
	"syscall"
)

const (
	niifInfo    uint32 = 1
	niifWarning uint32 = 2
	niifError   uint32 = 3
)

type trayCallbacks struct {
	OnOpenUI          func()
	IsAutostartOn     func() bool
	OnToggleAutostart func() (enabled bool, statusMsg string, err error)
	OnCheckAndUpdate  func(showBalloon func(title, msg string, flags uint32), confirm func(title, msg string) bool)
	GetAvailableTag   func() string
	OnExitAll         func()
}

func runDesktopTrayLoop(cb trayCallbacks) error {
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	<-ctx.Done()
	if cb.OnExitAll != nil {
		cb.OnExitAll()
	}
	return nil
}

func stopDesktopTrayLoop() {}

func notifyDialog(title, body string, isError bool) {}
