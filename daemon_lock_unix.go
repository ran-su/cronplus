//go:build darwin || linux

package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"syscall"
	"time"
)

var errDaemonAlreadyRunning = errors.New("another CronPlus daemon is already running")

type daemonLock struct {
	file *os.File
}

func acquireDaemonLock(path string, port int) (*daemonLock, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, fmt.Errorf("failed to create config dir: %w", err)
	}

	file, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, fmt.Errorf("failed to open daemon lock: %w", err)
	}

	if err := syscall.Flock(int(file.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		_ = file.Close()
		if isLockBusy(err) {
			return nil, fmt.Errorf("%w. Use `cronplus status` to talk to the running daemon, or stop it before starting another one", errDaemonAlreadyRunning)
		}
		return nil, fmt.Errorf("failed to acquire daemon lock: %w", err)
	}

	if err := writeDaemonLockInfo(file, port); err != nil {
		_ = syscall.Flock(int(file.Fd()), syscall.LOCK_UN)
		_ = file.Close()
		return nil, err
	}

	return &daemonLock{file: file}, nil
}

func (l *daemonLock) Release() {
	if l == nil || l.file == nil {
		return
	}
	_ = l.file.Truncate(0)
	_ = syscall.Flock(int(l.file.Fd()), syscall.LOCK_UN)
	_ = l.file.Close()
	l.file = nil
}

func isLockBusy(err error) bool {
	return errors.Is(err, syscall.EWOULDBLOCK) || errors.Is(err, syscall.EAGAIN)
}

func writeDaemonLockInfo(file *os.File, port int) error {
	if err := file.Truncate(0); err != nil {
		return fmt.Errorf("failed to update daemon lock: %w", err)
	}
	if _, err := file.Seek(0, 0); err != nil {
		return fmt.Errorf("failed to update daemon lock: %w", err)
	}
	_, err := fmt.Fprintf(file, "pid=%d\nport=%d\nstarted_at=%s\n", os.Getpid(), port, time.Now().Format(time.RFC3339))
	if err != nil {
		return fmt.Errorf("failed to update daemon lock: %w", err)
	}
	return nil
}
