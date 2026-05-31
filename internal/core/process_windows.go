//go:build windows

package core

import (
	"os"
	"os/exec"
	"time"

	"github.com/ran-su/cronplus/internal/models"
)

func configureProcessGroup(cmd *exec.Cmd) {}

func processGroupID(pid int) (int, error) { return pid, nil }

func processHasGroup(pid, pgid int) bool { return false }

func terminateProcessGroup(pgid int, grace time.Duration) models.RunCleanupDiagnostics {
	return models.RunCleanupDiagnostics{}
}

func cleanupDetachedProcesses(runDir string, grace time.Duration) (int, string) {
	return 0, ""
}

func processExists(pid int) bool { return false }

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
	a.RunDirectoryRemoved = a.RunDirectoryRemoved || b.RunDirectoryRemoved
	return a
}
