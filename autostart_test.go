package main

import (
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
