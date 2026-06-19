package core

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/ran-su/cronplus/internal/models"
)

func prepareBrowserRuntime(m *models.ScriptManifest, manifestDir, runDir string) (models.BrowserRunDiagnostics, map[string]string) {
	policy := m.Runtime.Browser
	if !policy.Enabled {
		return models.BrowserRunDiagnostics{}, nil
	}
	diag := models.BrowserRunDiagnostics{
		Enabled:               true,
		ProfileMode:           policy.ProfileMode,
		DownloadsMode:         policy.DownloadsMode,
		CachePolicy:           policy.CachePolicy,
		CleanupPolicy:         policy.CleanupPolicy,
		ProcessDetectionHints: append([]string(nil), policy.ProcessDetectionHints...),
	}
	env := map[string]string{}
	if policy.ProfileSource != "" {
		diag.ProfileSource = resolveManifestPath(manifestDir, policy.ProfileSource)
	}
	if runDir != "" {
		diag.ProfilePath = filepath.Join(runDir, "browser-profile")
		diag.DownloadPath = filepath.Join(runDir, "downloads")
		diag.CachePath = filepath.Join(runDir, "cache", "browser")
	}
	if policy.ProfileMode == "shared_external" {
		diag.ProfilePath = diag.ProfileSource
	}
	if policy.DownloadsMode == "default" {
		diag.DownloadPath = ""
	}
	if policy.CachePolicy == "default" || policy.CachePolicy == "disabled" {
		diag.CachePath = ""
	}
	setBrowserEnv(env, diag)
	clearBrowserEnvForDefaultPolicies(env, policy)
	if policy.ProfileMode == "copy_from" && diag.ProfileSource != "" && diag.ProfilePath != "" {
		if err := copyDirectory(diag.ProfileSource, diag.ProfilePath); err != nil {
			diag.ProfileCopyError = err.Error()
		} else {
			diag.ProfileCopied = true
		}
	}
	return diag, env
}

func browserProfileCopyFailure(diag models.BrowserRunDiagnostics) error {
	if !diag.Enabled || diag.ProfileCopyError == "" {
		return nil
	}
	return fmt.Errorf("browser profile copy failed: %s", diag.ProfileCopyError)
}

func finalizeBrowserDiagnostics(diag *models.BrowserRunDiagnostics, status string, stdoutBytes, stderrBytes int64, cleanup models.RunCleanupDiagnostics) bool {
	if diag == nil || !diag.Enabled {
		return false
	}
	diag.OutputBytes = stdoutBytes + stderrBytes
	diag.SuspectedLeftoverProcesses = cleanup.DetachedProcessesKilled
	if cleanup.OrphanScanError != "" {
		diag.CleanupStatus = "scan_failed"
	} else if cleanup.RunDirectoryCleanupError != "" {
		diag.CleanupStatus = "cleanup_failed"
	} else {
		diag.CleanupStatus = "clean"
	}
	retain, reason := shouldRetainBrowserRunDirectory(*diag, status)
	diag.RunDirectoryRetained = retain
	diag.RunDirectoryRetentionReason = reason
	if retain && diag.CleanupStatus == "clean" {
		diag.CleanupStatus = "retained"
	}
	return retain
}

func shouldRetainBrowserRunDirectory(diag models.BrowserRunDiagnostics, status string) (bool, string) {
	if diag.ProfileCopyError != "" && diag.CleanupPolicy != "delete_always" {
		return true, "profile copy failed"
	}
	switch diag.CleanupPolicy {
	case "keep_always":
		return true, "cleanup_policy keep_always"
	case "keep_on_failure":
		if status != "success" {
			return true, "cleanup_policy keep_on_failure"
		}
	case "delete_on_success":
		if status != "success" {
			return true, "cleanup_policy delete_on_success"
		}
	case "delete_always":
		return false, ""
	}
	return false, ""
}

func browserRunEnvironment(diag models.BrowserRunDiagnostics) map[string]string {
	env := map[string]string{}
	setBrowserEnv(env, diag)
	return env
}

func clearBrowserEnvForDefaultPolicies(env map[string]string, policy models.BrowserPolicy) {
	if policy.DownloadsMode == "default" {
		env["CRONPLUS_BROWSER_DOWNLOADS_DIR"] = ""
	}
	if policy.CachePolicy == "default" || policy.CachePolicy == "disabled" {
		env["CRONPLUS_BROWSER_CACHE_DIR"] = ""
	}
	if policy.ProfileMode == "shared_external" && policy.ProfileSource == "" {
		env["CRONPLUS_BROWSER_USER_DATA_DIR"] = ""
	}
}

func setBrowserEnv(env map[string]string, diag models.BrowserRunDiagnostics) {
	if diag.ProfilePath != "" {
		env["CRONPLUS_BROWSER_USER_DATA_DIR"] = diag.ProfilePath
	}
	if diag.DownloadPath != "" {
		env["CRONPLUS_BROWSER_DOWNLOADS_DIR"] = diag.DownloadPath
	}
	if diag.CachePath != "" {
		env["CRONPLUS_BROWSER_CACHE_DIR"] = diag.CachePath
	}
	if diag.ProfileMode != "" {
		env["CRONPLUS_BROWSER_PROFILE_MODE"] = diag.ProfileMode
	}
	if diag.ProfileSource != "" {
		env["CRONPLUS_BROWSER_PROFILE_SOURCE"] = diag.ProfileSource
	}
	if diag.DownloadsMode != "" {
		env["CRONPLUS_BROWSER_DOWNLOADS_MODE"] = diag.DownloadsMode
	}
	if diag.CachePolicy != "" {
		env["CRONPLUS_BROWSER_CACHE_POLICY"] = diag.CachePolicy
	}
	if diag.CleanupPolicy != "" {
		env["CRONPLUS_BROWSER_CLEANUP_POLICY"] = diag.CleanupPolicy
	}
	if len(diag.ProcessDetectionHints) > 0 {
		env["CRONPLUS_BROWSER_PROCESS_HINTS"] = strings.Join(diag.ProcessDetectionHints, ",")
	}
}

func resolveManifestPath(manifestDir, path string) string {
	path = strings.TrimSpace(path)
	if path == "" || filepath.IsAbs(path) {
		return path
	}
	return filepath.Join(manifestDir, path)
}

func copyDirectory(src, dst string) error {
	src = filepath.Clean(src)
	dst = filepath.Clean(dst)
	if src == dst {
		return fmt.Errorf("source and destination are the same directory: %s", src)
	}
	info, err := os.Stat(src)
	if err != nil {
		return err
	}
	if !info.IsDir() {
		return fmt.Errorf("source is not a directory: %s", src)
	}
	if err := os.RemoveAll(dst); err != nil {
		return err
	}
	return filepath.WalkDir(src, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		target := filepath.Join(dst, rel)
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if entry.Type()&os.ModeSymlink != 0 {
			linkTarget, err := os.Readlink(path)
			if err != nil {
				return err
			}
			return os.Symlink(linkTarget, target)
		}
		if entry.IsDir() {
			return os.MkdirAll(target, info.Mode().Perm())
		}
		return copyFile(path, target, info.Mode().Perm())
	})
}

func copyFile(src, dst string, perm os.FileMode) error {
	if err := os.MkdirAll(filepath.Dir(dst), 0700); err != nil {
		return err
	}
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, perm)
	if err != nil {
		return err
	}
	_, copyErr := io.Copy(out, in)
	closeErr := out.Close()
	if copyErr != nil {
		return copyErr
	}
	return closeErr
}
