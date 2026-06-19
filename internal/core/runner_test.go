package core

import (
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/ran-su/cronplus/internal/models"
)

func TestRunScriptParsesResultBeforeStdoutTruncation(t *testing.T) {
	python, err := exec.LookPath("python3")
	if err != nil {
		t.Skip("python3 not available")
	}

	dir := t.TempDir()
	script := `import json
print("x" * 2048)
print("CRONPLUS_RESULT=" + json.dumps({"status": "success", "summary": "tail result"}))
`
	if err := os.WriteFile(filepath.Join(dir, "script.py"), []byte(script), 0644); err != nil {
		t.Fatalf("write script: %v", err)
	}

	outcome := RunScript(&models.ScriptManifest{
		Script: models.ScriptSection{Path: "./script.py"},
		Runtime: models.RuntimeSection{
			TimeoutSeconds: 5,
			MaxOutputKB:    1,
			Environment: models.EnvironmentConfig{
				Strategy:          "system",
				PythonInterpreter: python,
			},
		},
		ResultContract: models.ResultContract{ResultPrefix: "CRONPLUS_RESULT="},
	}, dir)

	if len(outcome.Stdout) > 1024 {
		t.Fatalf("stdout len = %d, want <= 1024", len(outcome.Stdout))
	}
	if !outcome.Diagnostics.StdoutTruncated || outcome.Diagnostics.OutputBytesDiscarded == 0 {
		t.Fatalf("diagnostics did not record truncated output: %+v", outcome.Diagnostics)
	}
	if outcome.ParsedResult == nil {
		t.Fatal("ParsedResult is nil")
	}
	if outcome.ParsedResult.Summary != "tail result" {
		t.Fatalf("ParsedResult.Summary = %q, want tail result", outcome.ParsedResult.Summary)
	}
}

func TestRunScriptLoadsEnvFileAndEnvSecretReference(t *testing.T) {
	python, err := exec.LookPath("python3")
	if err != nil {
		t.Skip("python3 not available")
	}

	dir := t.TempDir()
	script := `import json, os
print("CRONPLUS_RESULT=" + json.dumps({
  "status": "success",
  "summary": os.environ.get("FROM_FILE", "") + "/" + os.environ.get("FROM_ENV", "") + "/" + os.environ.get("FROM_MANIFEST", "")
}))
`
	if err := os.WriteFile(filepath.Join(dir, "script.py"), []byte(script), 0644); err != nil {
		t.Fatalf("write script: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, ".env"), []byte("export FROM_FILE=file-value\nFROM_MANIFEST=file-value\n"), 0600); err != nil {
		t.Fatalf("write env file: %v", err)
	}
	t.Setenv("FROM_FILE", "process-value")
	t.Setenv("SOURCE_SECRET", "env-value")

	outcome := RunScript(&models.ScriptManifest{
		Script: models.ScriptSection{Path: "./script.py"},
		Runtime: models.RuntimeSection{
			TimeoutSeconds: 5,
			MaxOutputKB:    64,
			EnvFile:        ".env",
			Env: map[string]models.EnvVar{
				"FROM_ENV":      {Type: "secret", Value: "env://SOURCE_SECRET"},
				"FROM_MANIFEST": {Type: "plain", Value: "manifest-value"},
			},
			Environment: models.EnvironmentConfig{
				Strategy:          "system",
				PythonInterpreter: python,
			},
		},
		ResultContract: models.ResultContract{ResultPrefix: "CRONPLUS_RESULT="},
	}, dir)

	if outcome.ParsedResult == nil {
		t.Fatal("ParsedResult is nil")
	}
	if got, want := outcome.ParsedResult.Summary, "file-value/env-value/manifest-value"; got != want {
		t.Fatalf("summary = %q, want %q", got, want)
	}
}

func TestRunScriptKillsProcessGroupOnTimeout(t *testing.T) {
	python, err := exec.LookPath("python3")
	if err != nil {
		t.Skip("python3 not available")
	}

	dir := t.TempDir()
	script := `import subprocess, sys, time
child = subprocess.Popen([sys.executable, "-c", "import time; time.sleep(30)"])
print("CHILD_PID=" + str(child.pid), flush=True)
time.sleep(30)
`
	if err := os.WriteFile(filepath.Join(dir, "script.py"), []byte(script), 0644); err != nil {
		t.Fatalf("write script: %v", err)
	}

	outcome := RunScript(&models.ScriptManifest{
		Script: models.ScriptSection{Path: "./script.py"},
		Runtime: models.RuntimeSection{
			TimeoutSeconds: 1,
			MaxOutputKB:    64,
			Environment: models.EnvironmentConfig{
				Strategy:          "system",
				PythonInterpreter: python,
			},
		},
		ResultContract: models.ResultContract{ResultPrefix: "CRONPLUS_RESULT="},
	}, dir)

	if !outcome.TimedOut {
		t.Fatalf("TimedOut = false, want true; outcome=%+v", outcome)
	}
	if !outcome.Diagnostics.Cleanup.ProcessGroupTerminated {
		t.Fatalf("process group was not terminated: %+v", outcome.Diagnostics.Cleanup)
	}
	pid := childPIDFromOutput(t, outcome.Stdout)
	waitForProcessExit(t, pid)
}

func TestRunScriptCleansDetachedProcessReferencingRunDir(t *testing.T) {
	python, err := exec.LookPath("python3")
	if err != nil {
		t.Skip("python3 not available")
	}

	dir := t.TempDir()
	script := `import json, os, subprocess, sys
run_dir = os.environ["CRONPLUS_RUN_DIR"]
child = subprocess.Popen([sys.executable, "-c", "import time; time.sleep(30)", run_dir], start_new_session=True)
print("CHILD_PID=" + str(child.pid), flush=True)
print("CRONPLUS_RESULT=" + json.dumps({
  "status": "success",
  "summary": str(os.path.isdir(run_dir) and os.environ.get("HOME", "").startswith(run_dir)).lower()
}), flush=True)
`
	if err := os.WriteFile(filepath.Join(dir, "script.py"), []byte(script), 0644); err != nil {
		t.Fatalf("write script: %v", err)
	}

	outcome := RunScript(&models.ScriptManifest{
		Script: models.ScriptSection{Path: "./script.py"},
		Runtime: models.RuntimeSection{
			TimeoutSeconds: 5,
			MaxOutputKB:    64,
			Environment: models.EnvironmentConfig{
				Strategy:          "system",
				PythonInterpreter: python,
			},
		},
		ResultContract: models.ResultContract{ResultPrefix: "CRONPLUS_RESULT="},
	}, dir)

	if outcome.ParsedResult == nil || outcome.ParsedResult.Summary != "true" {
		t.Fatalf("parsed result = %+v, want summary true", outcome.ParsedResult)
	}
	if outcome.Diagnostics.RunDirectory == "" {
		t.Fatal("RunDirectory is empty")
	}
	if !outcome.Diagnostics.Cleanup.RunDirectoryRemoved {
		t.Fatalf("run directory was not removed: %+v", outcome.Diagnostics.Cleanup)
	}
	pid := childPIDFromOutput(t, outcome.Stdout)
	if outcome.Diagnostics.Cleanup.DetachedProcessesKilled == 0 {
		if outcome.Diagnostics.Cleanup.OrphanScanError != "" {
			if proc, err := os.FindProcess(pid); err == nil {
				_ = proc.Kill()
			}
			t.Skipf("detached process scan unavailable in this sandbox: %s", outcome.Diagnostics.Cleanup.OrphanScanError)
		}
		t.Fatalf("detached process cleanup did not run: %+v", outcome.Diagnostics.Cleanup)
	}
	if _, err := os.Stat(outcome.Diagnostics.RunDirectory); !os.IsNotExist(err) {
		t.Fatalf("run dir still exists or stat failed unexpectedly: %v", err)
	}
	waitForProcessExit(t, pid)
}

func TestRunScriptBrowserPolicyInjectsPathsAndCopiesProfile(t *testing.T) {
	python, err := exec.LookPath("python3")
	if err != nil {
		t.Skip("python3 not available")
	}

	dir := t.TempDir()
	sourceProfile := filepath.Join(dir, "source-profile")
	if err := os.MkdirAll(sourceProfile, 0700); err != nil {
		t.Fatalf("create source profile: %v", err)
	}
	if err := os.WriteFile(filepath.Join(sourceProfile, "Cookies"), []byte("cookie-data"), 0600); err != nil {
		t.Fatalf("write source profile: %v", err)
	}
	script := `import json, os
profile = os.environ["CRONPLUS_BROWSER_USER_DATA_DIR"]
downloads = os.environ["CRONPLUS_BROWSER_DOWNLOADS_DIR"]
cache = os.environ["CRONPLUS_BROWSER_CACHE_DIR"]
print("CRONPLUS_RESULT=" + json.dumps({
  "status": "success",
  "summary": str(os.path.exists(os.path.join(profile, "Cookies")) and downloads.startswith(os.environ["CRONPLUS_RUN_DIR"]) and cache.startswith(os.environ["CRONPLUS_RUN_DIR"])).lower()
}))
`
	if err := os.WriteFile(filepath.Join(dir, "script.py"), []byte(script), 0644); err != nil {
		t.Fatalf("write script: %v", err)
	}

	outcome := RunScript(&models.ScriptManifest{
		Script: models.ScriptSection{Path: "./script.py"},
		Runtime: models.RuntimeSection{
			TimeoutSeconds: 5,
			MaxOutputKB:    64,
			Environment: models.EnvironmentConfig{
				Strategy:          "system",
				PythonInterpreter: python,
			},
			Browser: models.BrowserPolicy{
				Enabled:       true,
				ProfileMode:   "copy_from",
				ProfileSource: sourceProfile,
			},
		},
		ResultContract: models.ResultContract{ResultPrefix: "CRONPLUS_RESULT="},
	}, dir)

	if outcome.ParsedResult == nil || outcome.ParsedResult.Summary != "true" {
		t.Fatalf("parsed result = %+v, want browser paths usable", outcome.ParsedResult)
	}
	if !outcome.Diagnostics.Browser.Enabled || !outcome.Diagnostics.Browser.ProfileCopied {
		t.Fatalf("browser diagnostics = %+v, want copied profile", outcome.Diagnostics.Browser)
	}
	if outcome.Diagnostics.Browser.ProfilePath == "" || outcome.Diagnostics.Browser.DownloadPath == "" || outcome.Diagnostics.Browser.CachePath == "" {
		t.Fatalf("browser paths missing: %+v", outcome.Diagnostics.Browser)
	}
	if outcome.Diagnostics.Browser.CleanupStatus == "cleanup_failed" || !outcome.Diagnostics.Cleanup.RunDirectoryRemoved {
		t.Fatalf("cleanup diagnostics = browser:%+v cleanup:%+v", outcome.Diagnostics.Browser, outcome.Diagnostics.Cleanup)
	}
}

func TestRunScriptBrowserPolicyCanRetainFailureDirectory(t *testing.T) {
	python, err := exec.LookPath("python3")
	if err != nil {
		t.Skip("python3 not available")
	}

	dir := t.TempDir()
	script := `import json
print("CRONPLUS_RESULT=" + json.dumps({"status": "failure", "summary": "keep"}))
`
	if err := os.WriteFile(filepath.Join(dir, "script.py"), []byte(script), 0644); err != nil {
		t.Fatalf("write script: %v", err)
	}

	outcome := RunScript(&models.ScriptManifest{
		Script: models.ScriptSection{Path: "./script.py"},
		Runtime: models.RuntimeSection{
			TimeoutSeconds: 5,
			MaxOutputKB:    64,
			Environment: models.EnvironmentConfig{
				Strategy:          "system",
				PythonInterpreter: python,
			},
			Browser: models.BrowserPolicy{
				Enabled:       true,
				CleanupPolicy: "keep_on_failure",
			},
		},
		ResultContract: models.ResultContract{ResultPrefix: "CRONPLUS_RESULT="},
	}, dir)

	if !outcome.Diagnostics.Browser.RunDirectoryRetained {
		t.Fatalf("browser diagnostics = %+v, want retained run dir", outcome.Diagnostics.Browser)
	}
	if _, err := os.Stat(outcome.Diagnostics.RunDirectory); err != nil {
		t.Fatalf("retained run dir missing: %v", err)
	}
	if err := os.RemoveAll(outcome.Diagnostics.RunDirectory); err != nil {
		t.Fatalf("cleanup retained run dir: %v", err)
	}
}

func TestRunScriptBrowserPolicyDefaultModesUnsetBrowserPaths(t *testing.T) {
	python, err := exec.LookPath("python3")
	if err != nil {
		t.Skip("python3 not available")
	}

	dir := t.TempDir()
	sourceProfile := filepath.Join(dir, "shared-profile")
	if err := os.MkdirAll(sourceProfile, 0700); err != nil {
		t.Fatalf("create shared profile: %v", err)
	}
	script := `import json, os
print("CRONPLUS_RESULT=" + json.dumps({
  "status": "success",
  "summary": "|".join([
    os.environ.get("CRONPLUS_BROWSER_USER_DATA_DIR", ""),
    os.environ.get("CRONPLUS_BROWSER_DOWNLOADS_DIR", "<missing>"),
    os.environ.get("CRONPLUS_BROWSER_CACHE_DIR", "<missing>")
  ])
}))
`
	if err := os.WriteFile(filepath.Join(dir, "script.py"), []byte(script), 0644); err != nil {
		t.Fatalf("write script: %v", err)
	}

	outcome := RunScript(&models.ScriptManifest{
		Script: models.ScriptSection{Path: "./script.py"},
		Runtime: models.RuntimeSection{
			TimeoutSeconds: 5,
			MaxOutputKB:    64,
			Environment: models.EnvironmentConfig{
				Strategy:          "system",
				PythonInterpreter: python,
			},
			Browser: models.BrowserPolicy{
				Enabled:       true,
				ProfileMode:   "shared_external",
				ProfileSource: sourceProfile,
				DownloadsMode: "default",
				CachePolicy:   "disabled",
			},
		},
		ResultContract: models.ResultContract{ResultPrefix: "CRONPLUS_RESULT="},
	}, dir)

	want := sourceProfile + "||"
	if outcome.ParsedResult == nil || outcome.ParsedResult.Summary != want {
		t.Fatalf("summary = %+v, want %q", outcome.ParsedResult, want)
	}
	if outcome.Diagnostics.Browser.DownloadPath != "" || outcome.Diagnostics.Browser.CachePath != "" {
		t.Fatalf("browser diagnostics = %+v, want default paths empty", outcome.Diagnostics.Browser)
	}
}

func TestRunScriptBrowserPolicyProfileCopyFailureDoesNotLaunchScript(t *testing.T) {
	python, err := exec.LookPath("python3")
	if err != nil {
		t.Skip("python3 not available")
	}

	dir := t.TempDir()
	missingProfile := filepath.Join(dir, "missing-profile")
	marker := filepath.Join(dir, "script-launched")
	script := `import os
with open(os.environ["MARKER"], "w", encoding="utf-8") as f:
    f.write("launched")
print('CRONPLUS_RESULT={"status":"success","summary":"launched"}')
`
	if err := os.WriteFile(filepath.Join(dir, "script.py"), []byte(script), 0644); err != nil {
		t.Fatalf("write script: %v", err)
	}

	outcome := RunScript(&models.ScriptManifest{
		Script: models.ScriptSection{Path: "./script.py"},
		Runtime: models.RuntimeSection{
			TimeoutSeconds: 5,
			MaxOutputKB:    64,
			Env: map[string]models.EnvVar{
				"MARKER": {Type: "plain", Value: marker},
			},
			Environment: models.EnvironmentConfig{
				Strategy:          "system",
				PythonInterpreter: python,
			},
			Browser: models.BrowserPolicy{
				Enabled:       true,
				ProfileMode:   "copy_from",
				ProfileSource: missingProfile,
			},
		},
		ResultContract: models.ResultContract{ResultPrefix: "CRONPLUS_RESULT="},
	}, dir)

	if outcome.ExitCode != -1 || outcome.Diagnostics.Browser.ProfileCopyError == "" {
		t.Fatalf("outcome = %+v, want launch failure with profile copy diagnostics", outcome)
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatalf("script marker exists or stat failed unexpectedly: %v", err)
	}
	if !outcome.Diagnostics.Browser.RunDirectoryRetained {
		t.Fatalf("browser diagnostics = %+v, want retained directory for copy failure", outcome.Diagnostics.Browser)
	}
	if err := os.RemoveAll(outcome.Diagnostics.RunDirectory); err != nil {
		t.Fatalf("cleanup retained run dir: %v", err)
	}
}

func childPIDFromOutput(t *testing.T, output string) int {
	t.Helper()
	for _, line := range strings.Split(output, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "CHILD_PID=") {
			pid, err := strconv.Atoi(strings.TrimPrefix(line, "CHILD_PID="))
			if err != nil {
				t.Fatalf("parse child pid %q: %v", line, err)
			}
			return pid
		}
	}
	t.Fatalf("missing CHILD_PID in output: %q", output)
	return 0
}

func waitForProcessExit(t *testing.T, pid int) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if !processExists(pid) {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("process %d still exists after cleanup deadline", pid)
}
