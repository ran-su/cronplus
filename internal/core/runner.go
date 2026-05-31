package core

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/ran-su/cronplus/internal/manifest"
	"github.com/ran-su/cronplus/internal/models"
)

type RunScriptOptions struct {
	TaskID     string
	RunID      string
	OnStarted  func(models.ActiveRunInfo)
	OnFinished func(runID string)
}

// RunScript executes a Python script and returns the outcome.
// It handles timeout enforcement, environment setup, and result parsing.
func RunScript(m *models.ScriptManifest, manifestDir string) *models.RunOutcome {
	return RunScriptWithOptions(m, manifestDir, RunScriptOptions{})
}

func RunScriptWithOptions(m *models.ScriptManifest, manifestDir string, opts RunScriptOptions) *models.RunOutcome {
	m.Defaults()
	if opts.RunID == "" {
		opts.RunID = generateID()
	}
	timeout := time.Duration(m.Runtime.TimeoutSeconds) * time.Second
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	grace := time.Duration(m.Runtime.ResourceLimits.GracefulKillSeconds) * time.Second
	if grace <= 0 {
		grace = 5 * time.Second
	}

	pythonExe := resolvePython(m, manifestDir)
	scriptPath := manifest.ResolveScriptPath(manifestDir, m)
	workingDir := manifest.ResolveWorkingDir(manifestDir, m)
	isolatedRun := m.RunIsolationEnabled()
	runDir := ""
	if isolatedRun {
		var err error
		runDir, err = prepareRunDirectory(opts.TaskID, opts.RunID)
		if err != nil {
			return launchFailureOutcome(m, pythonExe, scriptPath, workingDir, runDir, fmt.Errorf("failed to prepare run directory: %w", err))
		}
	}

	diagnostics := models.RunDiagnostics{
		PythonExecutable:    pythonExe,
		ScriptPath:          scriptPath,
		WorkingDirectory:    workingDir,
		EnvironmentStrategy: m.Runtime.Environment.Strategy,
		RequirementsFile:    m.Runtime.Environment.RequirementsFile,
		EnvFile:             m.Runtime.EnvFile,
		TimeoutSeconds:      m.Runtime.TimeoutSeconds,
		MaxOutputKB:         m.Runtime.MaxOutputKB,
		RunDirectory:        runDir,
		IsolatedRun:         isolatedRun,
	}

	cmd, limitMode := commandForRun(pythonExe, scriptPath, m.Runtime.ResourceLimits)
	diagnostics.LimitMode = limitMode
	cmd.Dir = workingDir
	cmd.Env = applyEnvOverrides(buildEnv(m, manifestDir), runEnvironment(opts.TaskID, opts.RunID, manifestDir, runDir, isolatedRun))
	configureProcessGroup(cmd)

	stdoutPipe, err := cmd.StdoutPipe()
	if err != nil {
		return launchFailureOutcome(m, pythonExe, scriptPath, workingDir, runDir, err)
	}
	stderrPipe, err := cmd.StderrPipe()
	if err != nil {
		return launchFailureOutcome(m, pythonExe, scriptPath, workingDir, runDir, err)
	}

	start := time.Now()
	if err := cmd.Start(); err != nil {
		return launchFailureOutcome(m, pythonExe, scriptPath, workingDir, runDir, err)
	}

	diagnostics.RootPID = cmd.Process.Pid
	if pgid, err := processGroupID(cmd.Process.Pid); err == nil {
		diagnostics.ProcessGroupID = pgid
	}

	startedReported := false
	if opts.OnStarted != nil {
		opts.OnStarted(models.ActiveRunInfo{
			TaskID:         opts.TaskID,
			RunID:          opts.RunID,
			RootPID:        diagnostics.RootPID,
			ProcessGroupID: diagnostics.ProcessGroupID,
			RunDirectory:   runDir,
			StartedAt:      start,
		})
		startedReported = true
	}
	defer func() {
		if startedReported && opts.OnFinished != nil {
			opts.OnFinished(opts.RunID)
		}
	}()

	maxBytes := m.Runtime.MaxOutputKB * 1024
	stdoutBuf := newCappedBuffer(maxBytes)
	stderrBuf := newCappedBuffer(maxBytes)
	resultCapture := newResultLineCapture(m.ResultContract.ResultPrefix)

	var copyWG sync.WaitGroup
	copyWG.Add(2)
	go func() {
		defer copyWG.Done()
		_, _ = io.Copy(io.MultiWriter(stdoutBuf, resultCapture), stdoutPipe)
	}()
	go func() {
		defer copyWG.Done()
		_, _ = io.Copy(stderrBuf, stderrPipe)
	}()

	waitCh := make(chan error, 1)
	go func() {
		waitCh <- cmd.Wait()
	}()

	var waitErr error
	timedOut := ctx.Err() == context.DeadlineExceeded
	select {
	case waitErr = <-waitCh:
	case <-ctx.Done():
		timedOut = true
		diagnostics.Cleanup = mergeCleanup(diagnostics.Cleanup, terminateProcessGroup(diagnostics.ProcessGroupID, grace))
		waitErr = <-waitCh
	}
	copyWG.Wait()
	durationMs := time.Since(start).Milliseconds()

	resultCapture.Finish()

	if diagnostics.ProcessGroupID > 1 {
		diagnostics.Cleanup = mergeCleanup(diagnostics.Cleanup, terminateProcessGroup(diagnostics.ProcessGroupID, grace))
	}
	if runDir != "" {
		killed, scanErr := cleanupDetachedProcesses(runDir, grace)
		diagnostics.Cleanup.DetachedProcessesKilled += killed
		diagnostics.Cleanup.OrphanScanError = scanErr
		if err := os.RemoveAll(runDir); err != nil {
			diagnostics.Cleanup.RunDirectoryCleanupError = err.Error()
		} else {
			diagnostics.Cleanup.RunDirectoryRemoved = true
		}
	}

	parsed := ParseResult(resultCapture.LastResultLine(), m.ResultContract.ResultPrefix)
	diagnostics.StructuredResultFound = parsed != nil
	diagnostics.StdoutBytes = stdoutBuf.Total()
	diagnostics.StderrBytes = stderrBuf.Total()
	diagnostics.StdoutTruncated = stdoutBuf.Truncated()
	diagnostics.StderrTruncated = stderrBuf.Truncated()
	diagnostics.OutputBytesDiscarded = stdoutBuf.Discarded() + stderrBuf.Discarded()

	stdoutStr := stdoutBuf.String()
	stderrStr := stderrBuf.String()

	if timedOut {
		stderrStr += fmt.Sprintf("\n[CronPlus] Script was terminated after %d-second timeout.", m.Runtime.TimeoutSeconds)
	}
	if diagnostics.Cleanup.ProcessGroupTerminated || diagnostics.Cleanup.ProcessGroupForceKilled || diagnostics.Cleanup.DetachedProcessesKilled > 0 {
		stderrStr += fmt.Sprintf("\n[CronPlus] Resource cleanup: process_group_terminated=%t process_group_force_killed=%t detached_processes_killed=%d.",
			diagnostics.Cleanup.ProcessGroupTerminated,
			diagnostics.Cleanup.ProcessGroupForceKilled,
			diagnostics.Cleanup.DetachedProcessesKilled,
		)
	}
	if diagnostics.OutputBytesDiscarded > 0 {
		stderrStr += fmt.Sprintf("\n[CronPlus] Output cap reached; discarded %d bytes.", diagnostics.OutputBytesDiscarded)
	}

	exitCode := 0
	if waitErr != nil {
		if exitErr, ok := waitErr.(*exec.ExitError); ok {
			exitCode = exitErr.ExitCode()
		} else if !timedOut {
			exitCode = -1
			stderrStr += fmt.Sprintf("\n[CronPlus] Launch error: %s", waitErr.Error())
		}
	}

	return &models.RunOutcome{
		ExitCode:     exitCode,
		Stdout:       stdoutStr,
		Stderr:       stderrStr,
		ParsedResult: parsed,
		TimedOut:     timedOut,
		DurationMs:   durationMs,
		Diagnostics:  diagnostics,
	}
}

func launchFailureOutcome(m *models.ScriptManifest, pythonExe, scriptPath, workingDir, runDir string, err error) *models.RunOutcome {
	if runDir != "" {
		_ = os.RemoveAll(runDir)
	}
	return &models.RunOutcome{
		ExitCode: -1,
		Stderr:   fmt.Sprintf("[CronPlus] Launch error: %s", err.Error()),
		Diagnostics: models.RunDiagnostics{
			PythonExecutable:    pythonExe,
			ScriptPath:          scriptPath,
			WorkingDirectory:    workingDir,
			EnvironmentStrategy: m.Runtime.Environment.Strategy,
			RequirementsFile:    m.Runtime.Environment.RequirementsFile,
			EnvFile:             m.Runtime.EnvFile,
			TimeoutSeconds:      m.Runtime.TimeoutSeconds,
			MaxOutputKB:         m.Runtime.MaxOutputKB,
			RunDirectory:        runDir,
			IsolatedRun:         m.RunIsolationEnabled(),
		},
	}
}

func resolvePython(m *models.ScriptManifest, manifestDir string) string {
	switch m.Runtime.Environment.Strategy {
	case "managed_venv":
		// Managed venv lives in .cronplus-venv inside the package directory
		venvDir := filepath.Join(manifestDir, ".cronplus-venv")
		return filepath.Join(venvDir, "bin", "python3")
	case "venv_path":
		if m.Runtime.Environment.VenvPath != "" {
			p := m.Runtime.Environment.VenvPath
			if !filepath.IsAbs(p) {
				p = filepath.Join(manifestDir, p)
			}
			return filepath.Join(p, "bin", "python3")
		}
		return "python3"
	default: // "system"
		if m.Runtime.Environment.PythonInterpreter != "" {
			return m.Runtime.Environment.PythonInterpreter
		}
		return "python3"
	}
}

func prepareRunDirectory(taskID, runID string) (string, error) {
	if taskID == "" {
		taskID = "task"
	}
	root := filepath.Join(os.TempDir(), "cronplus-runs")
	name := sanitizePathPart(taskID) + "-" + sanitizePathPart(runID)
	runDir := filepath.Join(root, name)
	for _, dir := range []string{
		runDir,
		filepath.Join(runDir, "tmp"),
		filepath.Join(runDir, "home"),
		filepath.Join(runDir, "cache"),
		filepath.Join(runDir, "config"),
		filepath.Join(runDir, "data"),
		filepath.Join(runDir, "downloads"),
		filepath.Join(runDir, "browser-profile"),
	} {
		if err := os.MkdirAll(dir, 0700); err != nil {
			return "", err
		}
	}
	return runDir, nil
}

func sanitizePathPart(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return "run"
	}
	var b strings.Builder
	for _, r := range s {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '-' || r == '_' {
			b.WriteRune(r)
		} else {
			b.WriteByte('-')
		}
	}
	cleaned := strings.Trim(b.String(), "-")
	if cleaned == "" {
		return "run"
	}
	return cleaned
}

func runEnvironment(taskID, runID, manifestDir, runDir string, isolated bool) map[string]string {
	env := map[string]string{
		"CRONPLUS_TASK_ID":  taskID,
		"CRONPLUS_RUN_ID":   runID,
		"CRONPLUS_TASK_DIR": manifestDir,
	}
	if runDir == "" {
		return env
	}

	env["CRONPLUS_RUN_DIR"] = runDir
	env["CRONPLUS_BROWSER_USER_DATA_DIR"] = filepath.Join(runDir, "browser-profile")
	env["CRONPLUS_BROWSER_DOWNLOADS_DIR"] = filepath.Join(runDir, "downloads")
	env["CRONPLUS_BROWSER_CACHE_DIR"] = filepath.Join(runDir, "cache", "browser")
	if isolated {
		env["TMPDIR"] = filepath.Join(runDir, "tmp")
		env["TMP"] = filepath.Join(runDir, "tmp")
		env["TEMP"] = filepath.Join(runDir, "tmp")
		env["HOME"] = filepath.Join(runDir, "home")
		env["XDG_CACHE_HOME"] = filepath.Join(runDir, "cache")
		env["XDG_CONFIG_HOME"] = filepath.Join(runDir, "config")
		env["XDG_DATA_HOME"] = filepath.Join(runDir, "data")
	}
	return env
}

func applyEnvOverrides(env []string, overrides map[string]string) []string {
	values := make(map[string]string)
	var order []string
	set := func(name, value string) {
		if name == "" {
			return
		}
		if _, exists := values[name]; !exists {
			order = append(order, name)
		}
		values[name] = value
	}
	for _, entry := range env {
		name, value, ok := strings.Cut(entry, "=")
		if ok {
			set(name, value)
		}
	}
	names := make([]string, 0, len(overrides))
	for name := range overrides {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		set(name, overrides[name])
	}

	result := make([]string, 0, len(order))
	for _, name := range order {
		result = append(result, name+"="+values[name])
	}
	return result
}

func buildEnv(m *models.ScriptManifest, manifestDir string) []string {
	values := make(map[string]string)
	var order []string
	set := func(name, value string) {
		if name == "" {
			return
		}
		if _, exists := values[name]; !exists {
			order = append(order, name)
		}
		values[name] = value
	}

	for _, entry := range os.Environ() {
		name, value, ok := strings.Cut(entry, "=")
		if ok {
			set(name, value)
		}
	}
	for _, entry := range loadEnvFile(m.Runtime.EnvFile, manifestDir) {
		name, value, ok := strings.Cut(entry, "=")
		if ok {
			set(name, value)
		}
	}

	names := make([]string, 0, len(m.Runtime.Env))
	for name := range m.Runtime.Env {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		v := m.Runtime.Env[name]
		switch v.Type {
		case "plain":
			set(name, v.Value)
		case "secret":
			if strings.HasPrefix(v.Value, "env://") {
				if value, ok := os.LookupEnv(strings.TrimPrefix(v.Value, "env://")); ok {
					set(name, value)
				} else {
					fmt.Printf("[CronPlus] Warning: secret env var %s was not injected; source env var not found.\n", name)
				}
			} else {
				fmt.Printf("[CronPlus] Warning: secret env var %s was not injected; use env://NAME or env_file.\n", name)
			}
		}
	}

	env := make([]string, 0, len(order))
	for _, name := range order {
		env = append(env, name+"="+values[name])
	}
	return env
}

func loadEnvFile(path, manifestDir string) []string {
	if path == "" {
		return nil
	}
	if !filepath.IsAbs(path) {
		path = filepath.Join(manifestDir, path)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		fmt.Printf("[CronPlus] Warning: env_file was not loaded: %v\n", err)
		return nil
	}

	var result []string
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		line = strings.TrimPrefix(line, "export ")
		key, value, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		key = strings.TrimSpace(key)
		value = strings.TrimSpace(value)
		value = strings.Trim(value, `"'`)
		if key != "" {
			result = append(result, key+"="+value)
		}
	}
	return result
}

type cappedBuffer struct {
	mu    sync.Mutex
	limit int
	data  []byte
	total int64
}

func newCappedBuffer(limit int) *cappedBuffer {
	if limit <= 0 {
		limit = 1
	}
	return &cappedBuffer{limit: limit}
}

func (b *cappedBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.total += int64(len(p))
	if len(b.data) < b.limit {
		remaining := b.limit - len(b.data)
		if remaining > len(p) {
			remaining = len(p)
		}
		b.data = append(b.data, p[:remaining]...)
	}
	return len(p), nil
}

func (b *cappedBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return string(append([]byte(nil), b.data...))
}

func (b *cappedBuffer) Total() int64 {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.total
}

func (b *cappedBuffer) Truncated() bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.total > int64(len(b.data))
}

func (b *cappedBuffer) Discarded() int64 {
	b.mu.Lock()
	defer b.mu.Unlock()
	discarded := b.total - int64(len(b.data))
	if discarded < 0 {
		return 0
	}
	return discarded
}

type resultLineCapture struct {
	prefix  string
	maxLine int
	current []byte
	dropped bool
	last    string
}

func newResultLineCapture(prefix string) *resultLineCapture {
	if prefix == "" {
		prefix = "CRONPLUS_RESULT="
	}
	return &resultLineCapture{prefix: prefix, maxLine: 1024 * 1024}
}

func (c *resultLineCapture) Write(p []byte) (int, error) {
	for _, ch := range p {
		if ch == '\n' {
			c.finishLine()
			continue
		}
		if len(c.current) < c.maxLine {
			c.current = append(c.current, ch)
		} else {
			c.dropped = true
		}
	}
	return len(p), nil
}

func (c *resultLineCapture) Finish() {
	if len(c.current) > 0 || c.dropped {
		c.finishLine()
	}
}

func (c *resultLineCapture) LastResultLine() string {
	return c.last
}

func (c *resultLineCapture) finishLine() {
	line := strings.TrimSpace(string(c.current))
	if !c.dropped && strings.HasPrefix(line, c.prefix) {
		c.last = line
	}
	c.current = nil
	c.dropped = false
}

// EnsureEnvironment sets up the Python environment for a task.
func EnsureEnvironment(m *models.ScriptManifest, manifestDir string) error {
	switch m.Runtime.Environment.Strategy {
	case "managed_venv":
		return ensureManagedVenv(m, manifestDir)
	default:
		return nil
	}
}

func ensureManagedVenv(m *models.ScriptManifest, manifestDir string) error {
	venvDir := filepath.Join(manifestDir, ".cronplus-venv")

	// Check if venv already exists
	pythonPath := filepath.Join(venvDir, "bin", "python3")
	if _, err := os.Stat(pythonPath); err == nil {
		// Install requirements if specified
		return installRequirements(m, manifestDir, venvDir)
	}

	// Create venv
	baseInterpreter := m.Runtime.Environment.PythonInterpreter
	if baseInterpreter == "" {
		baseInterpreter = "python3"
	}

	fmt.Printf("[CronPlus] Creating managed venv at %s using %s\n", venvDir, baseInterpreter)
	cmd := exec.Command(baseInterpreter, "-m", "venv", venvDir)
	if err := runSetupCommand(m, manifestDir, cmd, "create managed venv"); err != nil {
		return err
	}

	return installRequirements(m, manifestDir, venvDir)
}

func installRequirements(m *models.ScriptManifest, manifestDir, venvDir string) error {
	reqFile := m.Runtime.Environment.RequirementsFile
	if reqFile == "" {
		return nil
	}

	if !filepath.IsAbs(reqFile) {
		reqFile = filepath.Join(manifestDir, reqFile)
	}

	if _, err := os.Stat(reqFile); os.IsNotExist(err) {
		return nil
	}

	pip := filepath.Join(venvDir, "bin", "pip")
	fmt.Printf("[CronPlus] Installing requirements from %s\n", reqFile)

	// Check if requirements are already satisfied by comparing mtime
	markerFile := filepath.Join(venvDir, ".cronplus-req-hash")
	reqContent, err := os.ReadFile(reqFile)
	if err != nil {
		return fmt.Errorf("failed to read requirements file: %w", err)
	}
	if marker, err := os.ReadFile(markerFile); err == nil {
		if string(marker) == string(reqContent) {
			return nil // Already up to date
		}
	}

	cmd := exec.Command(pip, "install", "-r", reqFile, "-q")
	if err := runSetupCommand(m, manifestDir, cmd, "pip install requirements"); err != nil {
		return err
	}

	if err := os.WriteFile(markerFile, reqContent, 0644); err != nil {
		return fmt.Errorf("failed to write requirements marker: %w", err)
	}
	return nil
}

func runSetupCommand(m *models.ScriptManifest, manifestDir string, cmd *exec.Cmd, description string) error {
	timeout := environmentSetupTimeout(m)
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	cmd.Dir = manifestDir
	configureProcessGroup(cmd)

	stdoutPipe, err := cmd.StdoutPipe()
	if err != nil {
		return fmt.Errorf("%s failed to prepare stdout: %w", description, err)
	}
	stderrPipe, err := cmd.StderrPipe()
	if err != nil {
		return fmt.Errorf("%s failed to prepare stderr: %w", description, err)
	}

	if err := cmd.Start(); err != nil {
		return fmt.Errorf("%s failed to start: %w", description, err)
	}

	pgid := 0
	if cmd.Process != nil {
		if got, err := processGroupID(cmd.Process.Pid); err == nil {
			pgid = got
		}
	}

	maxOutputKB := m.Runtime.MaxOutputKB
	if maxOutputKB <= 0 {
		maxOutputKB = 512
	}
	maxBytes := maxOutputKB * 1024
	stdoutBuf := newCappedBuffer(maxBytes)
	stderrBuf := newCappedBuffer(maxBytes)

	var copyWG sync.WaitGroup
	copyWG.Add(2)
	go func() {
		defer copyWG.Done()
		_, _ = io.Copy(stdoutBuf, stdoutPipe)
	}()
	go func() {
		defer copyWG.Done()
		_, _ = io.Copy(stderrBuf, stderrPipe)
	}()

	waitCh := make(chan error, 1)
	go func() {
		waitCh <- cmd.Wait()
	}()

	var waitErr error
	timedOut := false
	select {
	case waitErr = <-waitCh:
	case <-ctx.Done():
		timedOut = true
		grace := time.Duration(m.Runtime.ResourceLimits.GracefulKillSeconds) * time.Second
		if grace <= 0 {
			grace = 5 * time.Second
		}
		_ = terminateProcessGroup(pgid, grace)
		if cmd.Process != nil {
			_ = cmd.Process.Kill()
		}
		waitErr = <-waitCh
	}
	copyWG.Wait()

	output := strings.TrimSpace(strings.Join([]string{stdoutBuf.String(), stderrBuf.String()}, "\n"))
	if stdoutBuf.Truncated() || stderrBuf.Truncated() {
		output += fmt.Sprintf("\n[CronPlus] Setup output cap reached; discarded %d bytes.", stdoutBuf.Discarded()+stderrBuf.Discarded())
	}
	if timedOut {
		return fmt.Errorf("%s timed out after %s\n%s", description, timeout, output)
	}
	if waitErr != nil {
		return fmt.Errorf("%s failed: %w\n%s", description, waitErr, output)
	}
	return nil
}

func environmentSetupTimeout(m *models.ScriptManifest) time.Duration {
	seconds := m.Runtime.TimeoutSeconds
	if seconds < 300 {
		seconds = 300
	}
	return time.Duration(seconds) * time.Second
}
