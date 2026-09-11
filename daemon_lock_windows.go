//go:build windows

package main

import (
	"errors"
	"os"

	"golang.org/x/sys/windows"
)

func tryLockDaemonFile(file *os.File) error {
	// Windows byte-range locks also prevent reads through other handles.
	// Lock beyond the metadata so status clients can still read the port.
	overlapped := windows.Overlapped{OffsetHigh: 1}
	return windows.LockFileEx(windows.Handle(file.Fd()), windows.LOCKFILE_EXCLUSIVE_LOCK|windows.LOCKFILE_FAIL_IMMEDIATELY, 0, 1, 0, &overlapped)
}

func unlockDaemonFile(file *os.File) {
	overlapped := windows.Overlapped{OffsetHigh: 1}
	_ = windows.UnlockFileEx(windows.Handle(file.Fd()), 0, 1, 0, &overlapped)
}

func isLockBusy(err error) bool {
	return errors.Is(err, windows.ERROR_LOCK_VIOLATION)
}
