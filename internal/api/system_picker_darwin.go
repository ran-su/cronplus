//go:build darwin

package api

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strings"
)

func pickDirectory(ctx context.Context) (directoryPickerResult, error) {
	script := `POSIX path of (choose folder with prompt "Select a CronPlus task package")`
	cmd := exec.CommandContext(ctx, "osascript", "-e", script)
	out, err := cmd.Output()
	if err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			stderr := strings.TrimSpace(string(exitErr.Stderr))
			if strings.Contains(stderr, "User canceled") || strings.Contains(stderr, "-128") {
				return directoryPickerResult{Canceled: true}, nil
			}
			if stderr != "" {
				return directoryPickerResult{}, fmt.Errorf("system directory picker failed: %s", stderr)
			}
		}
		return directoryPickerResult{}, fmt.Errorf("system directory picker failed: %w", err)
	}

	path := strings.TrimSpace(string(out))
	if path == "" {
		return directoryPickerResult{Canceled: true}, nil
	}
	return directoryPickerResult{Path: path}, nil
}
