package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRenderLaunchAgentPlist(t *testing.T) {
	plist := renderLaunchAgentPlist(launchAgentOptions{
		Label:      launchAgentLabel,
		BinaryPath: "/Applications/CronPlus & Tools/cronplus",
		HomeDir:    "/Users/ran&su",
		LogPath:    "/Users/ran&su/Library/Logs/cronplus.log",
		Port:       9887,
		Environment: map[string]string{
			"CRONPLUS_MAX_CONCURRENT_RUNS": "2",
		},
	})

	wants := []string{
		"<string>com.cronplus.daemon</string>",
		"<string>/Applications/CronPlus &amp; Tools/cronplus</string>",
		"<string>--port</string>",
		"<string>9887</string>",
		"<string>/Users/ran&amp;su/Library/Logs/cronplus.log</string>",
		"<key>CRONPLUS_MAX_CONCURRENT_RUNS</key>",
		"<string>2</string>",
		"<key>HOME</key>",
		"<string>/Users/ran&amp;su</string>",
		"<key>PATH</key>",
	}
	for _, want := range wants {
		if !strings.Contains(plist, want) {
			t.Fatalf("plist missing %q:\n%s", want, plist)
		}
	}
	if strings.Contains(plist, "/Applications/CronPlus & Tools/cronplus") {
		t.Fatalf("plist contains unescaped binary path:\n%s", plist)
	}
}

func TestLaunchAgentPaths(t *testing.T) {
	home := "/Users/example"
	if got := launchAgentPath(home); got != "/Users/example/Library/LaunchAgents/com.cronplus.daemon.plist" {
		t.Fatalf("launchAgentPath() = %q", got)
	}
	if got := launchAgentLogPath(home); got != "/Users/example/Library/Logs/cronplus.log" {
		t.Fatalf("launchAgentLogPath() = %q", got)
	}
}

func TestValidateLaunchBinaryRejectsLauncherScript(t *testing.T) {
	path := writeTestLaunchFile(t, "script-cronplus", []byte("#!/bin/sh\nexec /tmp/cronplus \"$@\"\n"), 0o755)

	err := validateLaunchBinary(path)
	if err == nil || !strings.Contains(err.Error(), "launcher script") {
		t.Fatalf("validateLaunchBinary() error = %v, want launcher script error", err)
	}
}

func TestValidateLaunchBinaryAcceptsExecutableFile(t *testing.T) {
	path := writeTestLaunchFile(t, "binary-cronplus", []byte{0x7f, 'E', 'L', 'F'}, 0o755)

	if err := validateLaunchBinary(path); err != nil {
		t.Fatalf("validateLaunchBinary() error = %v", err)
	}
}

func TestValidateLaunchBinaryRejectsNonExecutableFile(t *testing.T) {
	path := writeTestLaunchFile(t, "nonexec-cronplus", []byte{0x7f, 'E', 'L', 'F'}, 0o644)

	err := validateLaunchBinary(path)
	if err == nil || !strings.Contains(err.Error(), "not executable") {
		t.Fatalf("validateLaunchBinary() error = %v, want executable error", err)
	}
}

func TestUnstableExecutablePathDetection(t *testing.T) {
	unstable := []string{
		"/private/tmp/cronplus",
		"/var/folders/aa/bb/T/cronplus",
		"/Users/ransu/Library/Caches/go-build/aa/bb/cronplus",
		"/Users/ransu/Documents/Codex/2026-05-31/install-github-ran-su-cronplus-for/work/cronplus-headminus1.aSHlKr/cronplus",
	}
	for _, path := range unstable {
		if !isLikelyUnstableExecutablePath(path) {
			t.Fatalf("isLikelyUnstableExecutablePath(%q) = false, want true", path)
		}
	}

	stable := []string{
		"/usr/local/bin/cronplus",
		"/opt/homebrew/bin/cronplus",
		"/Users/ransu/.local/bin/cronplus",
	}
	for _, path := range stable {
		if isLikelyUnstableExecutablePath(path) {
			t.Fatalf("isLikelyUnstableExecutablePath(%q) = true, want false", path)
		}
	}
}

func writeTestLaunchFile(t *testing.T, name string, data []byte, perm os.FileMode) string {
	t.Helper()
	dir := filepath.Join(".cache", "autostart-test", t.Name())
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = os.RemoveAll(filepath.Join(".cache", "autostart-test"))
	})
	path, err := filepath.Abs(filepath.Join(dir, name))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, perm); err != nil {
		t.Fatal(err)
	}
	return path
}
