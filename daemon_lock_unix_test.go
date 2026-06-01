//go:build darwin || linux

package main

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestAcquireDaemonLockAllowsOnlyOneHolder(t *testing.T) {
	lockPath := filepath.Join(t.TempDir(), "daemon.lock")

	first, err := acquireDaemonLock(lockPath, 9887)
	if err != nil {
		t.Fatalf("first acquireDaemonLock() error = %v", err)
	}
	defer first.Release()

	second, err := acquireDaemonLock(lockPath, 9876)
	if err == nil {
		second.Release()
		t.Fatal("second acquireDaemonLock() unexpectedly succeeded")
	}
	if !errors.Is(err, errDaemonAlreadyRunning) {
		t.Fatalf("second acquireDaemonLock() error = %v, want errDaemonAlreadyRunning", err)
	}

	data, err := os.ReadFile(lockPath)
	if err != nil {
		t.Fatalf("ReadFile(lockPath) error = %v", err)
	}
	if !strings.Contains(string(data), "pid=") || !strings.Contains(string(data), "port=9887") || !strings.Contains(string(data), "started_at=") {
		t.Fatalf("lock file missing diagnostic data:\n%s", string(data))
	}
	if port, err := readDaemonLockPort(lockPath); err != nil || port != 9887 {
		t.Fatalf("readDaemonLockPort() = %d, %v; want 9887, nil", port, err)
	}
}

func TestAcquireDaemonLockCanBeReacquiredAfterRelease(t *testing.T) {
	lockPath := filepath.Join(t.TempDir(), "daemon.lock")

	first, err := acquireDaemonLock(lockPath, 9876)
	if err != nil {
		t.Fatalf("first acquireDaemonLock() error = %v", err)
	}
	first.Release()

	second, err := acquireDaemonLock(lockPath, 9876)
	if err != nil {
		t.Fatalf("second acquireDaemonLock() error = %v", err)
	}
	second.Release()
}
