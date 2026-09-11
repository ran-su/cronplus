//go:build windows

package core

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"syscall"
	"time"

	"github.com/ran-su/cronplus/internal/models"
	"golang.org/x/sys/windows"
)

func configureProcessGroup(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: windows.CREATE_NEW_PROCESS_GROUP}
}

func processGroupID(pid int) (int, error) { return pid, nil }

func processHasGroup(pid, pgid int) bool { return false }

func terminateProcessGroup(pgid int, grace time.Duration) models.RunCleanupDiagnostics {
	var cleanup models.RunCleanupDiagnostics
	if !processExists(pgid) {
		return cleanup
	}
	// taskkill can terminate the descendants while their parent is alive.
	// Detached processes and children of an exited parent are not covered.
	if systemDir, err := windows.GetSystemDirectory(); err == nil {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		cmd := exec.CommandContext(ctx, filepath.Join(systemDir, "taskkill.exe"), "/PID", strconv.Itoa(pgid), "/T", "/F")
		cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
		if err := cmd.Run(); err == nil {
			cleanup.ProcessGroupTerminated = true
			cleanup.ProcessGroupForceKilled = true
			return cleanup
		}
	}
	if process, err := os.FindProcess(pgid); err == nil {
		defer process.Release()
		if err := process.Kill(); err == nil {
			cleanup.ProcessGroupForceKilled = true
		}
	}
	return cleanup
}

func cleanupDetachedProcesses(runDir string, grace time.Duration) (int, string) {
	return 0, ""
}

func processExists(pid int) bool {
	if pid <= 1 || pid == os.Getpid() {
		return false
	}
	handle, err := windows.OpenProcess(windows.SYNCHRONIZE, false, uint32(pid))
	if err != nil {
		return false
	}
	defer windows.CloseHandle(handle)
	status, err := windows.WaitForSingleObject(handle, 0)
	return err == nil && status == uint32(windows.WAIT_TIMEOUT)
}

func commandForRun(pythonExe, scriptPath string, limits models.ResourceLimits) (*exec.Cmd, string) {
	return exec.Command(pythonExe, scriptPath), ""
}

func cleanupPersistedRunProcess(info models.ActiveRunInfo, grace time.Duration) models.RunCleanupDiagnostics {
	var cleanup models.RunCleanupDiagnostics
	if info.RunDirectory != "" {
		if err := os.RemoveAll(info.RunDirectory); err != nil {
			cleanup.RunDirectoryCleanupError = err.Error()
		} else {
			cleanup.RunDirectoryRemoved = true
		}
	}
	return cleanup
}

func mergeCleanup(a, b models.RunCleanupDiagnostics) models.RunCleanupDiagnostics {
	a.ProcessGroupTerminated = a.ProcessGroupTerminated || b.ProcessGroupTerminated
	a.ProcessGroupForceKilled = a.ProcessGroupForceKilled || b.ProcessGroupForceKilled
	a.DetachedProcessesKilled += b.DetachedProcessesKilled
	if a.RunDirectoryCleanupError == "" {
		a.RunDirectoryCleanupError = b.RunDirectoryCleanupError
	}
	if a.OrphanScanError == "" {
		a.OrphanScanError = b.OrphanScanError
	}
	a.RunDirectoryRemoved = a.RunDirectoryRemoved || b.RunDirectoryRemoved
	return a
}
