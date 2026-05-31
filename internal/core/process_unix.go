//go:build !windows

package core

import (
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/ran-su/cronplus/internal/models"
)

func configureProcessGroup(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
}

func processGroupID(pid int) (int, error) {
	return syscall.Getpgid(pid)
}

func processHasGroup(pid, pgid int) bool {
	if pid <= 1 || pgid <= 1 {
		return false
	}
	got, err := syscall.Getpgid(pid)
	return err == nil && got == pgid
}

func terminateProcessGroup(pgid int, grace time.Duration) models.RunCleanupDiagnostics {
	var cleanup models.RunCleanupDiagnostics
	if pgid <= 1 {
		return cleanup
	}
	if err := syscall.Kill(-pgid, 0); err != nil {
		return cleanup
	}

	if err := syscall.Kill(-pgid, syscall.SIGTERM); err == nil {
		cleanup.ProcessGroupTerminated = true
	}
	deadline := time.Now().Add(grace)
	for time.Now().Before(deadline) {
		if err := syscall.Kill(-pgid, 0); err != nil {
			return cleanup
		}
		time.Sleep(50 * time.Millisecond)
	}
	if err := syscall.Kill(-pgid, syscall.SIGKILL); err == nil {
		cleanup.ProcessGroupForceKilled = true
	}
	return cleanup
}

func cleanupDetachedProcesses(runDir string, grace time.Duration) (int, string) {
	if runDir == "" {
		return 0, ""
	}
	pids, err := processesReferencingPath(runDir)
	if err != nil {
		return 0, err.Error()
	}
	if len(pids) == 0 {
		return 0, ""
	}

	for _, pid := range pids {
		_ = syscall.Kill(pid, syscall.SIGTERM)
	}
	deadline := time.Now().Add(grace)
	for time.Now().Before(deadline) {
		remaining := 0
		for _, pid := range pids {
			if processExists(pid) {
				remaining++
			}
		}
		if remaining == 0 {
			return len(pids), ""
		}
		time.Sleep(50 * time.Millisecond)
	}
	for _, pid := range pids {
		_ = syscall.Kill(pid, syscall.SIGKILL)
	}
	return len(pids), ""
}

func processExists(pid int) bool {
	if pid <= 1 || pid == os.Getpid() {
		return false
	}
	err := syscall.Kill(pid, 0)
	return err == nil || err == syscall.EPERM
}

func processesReferencingPath(path string) ([]int, error) {
	out, err := exec.Command("ps", "-axo", "pid=,command=").Output()
	if err != nil {
		return nil, err
	}
	var pids []int
	for _, line := range strings.Split(string(out), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || !strings.Contains(line, path) {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) == 0 {
			continue
		}
		pid, err := strconv.Atoi(fields[0])
		if err != nil || pid <= 1 || pid == os.Getpid() {
			continue
		}
		pids = append(pids, pid)
	}
	return pids, nil
}

func commandForRun(pythonExe, scriptPath string, limits models.ResourceLimits) (*exec.Cmd, string) {
	prefix := resourceLimitShellPrefix(limits)
	if prefix == "" {
		return exec.Command(pythonExe, scriptPath), ""
	}
	return exec.Command("/bin/sh", "-c", prefix+`; exec "$@"`, "cronplus-task", pythonExe, scriptPath), "ulimit"
}

func resourceLimitShellPrefix(limits models.ResourceLimits) string {
	var parts []string
	if limits.MaxOpenFiles > 0 {
		parts = append(parts, fmt.Sprintf("ulimit -n %d 2>/dev/null || true", limits.MaxOpenFiles))
	}
	if limits.MaxCPUSeconds > 0 {
		parts = append(parts, fmt.Sprintf("ulimit -t %d 2>/dev/null || true", limits.MaxCPUSeconds))
	}
	if limits.MaxProcesses > 0 {
		parts = append(parts, fmt.Sprintf("ulimit -u %d 2>/dev/null || true", limits.MaxProcesses))
	}
	if limits.MaxMemoryMB > 0 {
		kb := limits.MaxMemoryMB * 1024
		parts = append(parts, fmt.Sprintf("ulimit -v %d 2>/dev/null || true", kb))
		parts = append(parts, fmt.Sprintf("ulimit -m %d 2>/dev/null || true", kb))
	}
	return strings.Join(parts, "; ")
}

func cleanupPersistedRunProcess(info models.ActiveRunInfo, grace time.Duration) models.RunCleanupDiagnostics {
	var cleanup models.RunCleanupDiagnostics
	if processHasGroup(info.RootPID, info.ProcessGroupID) {
		cleanup = mergeCleanup(cleanup, terminateProcessGroup(info.ProcessGroupID, grace))
	}
	killed, scanErr := cleanupDetachedProcesses(info.RunDirectory, grace)
	cleanup.DetachedProcessesKilled += killed
	cleanup.OrphanScanError = scanErr
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
