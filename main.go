package main

import (
	"bytes"
	"context"
	"embed"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"log"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/ran-su/cronplus/internal/api"
	"github.com/ran-su/cronplus/internal/core"
	"github.com/ran-su/cronplus/internal/delivery"
	"github.com/ran-su/cronplus/internal/inbound"
	"github.com/ran-su/cronplus/internal/manifest"
	"github.com/ran-su/cronplus/internal/models"
	"github.com/ran-su/cronplus/internal/store"
)

//go:embed web/* schemas/*.json
var webContent embed.FS

// version is set via -ldflags at build time.
var version = "dev"

func main() {
	if handled, code := runCLICommand(os.Args[1:]); handled {
		os.Exit(code)
	}

	port := flag.Int("port", 0, "HTTP port (default 9876, or CRONPLUS_PORT env)")
	flag.Parse()

	log.SetFlags(log.Ltime)
	log.Println("[CronPlus] Starting...")

	// Resolve port: flag > env > default
	listenPort := 9876
	if *port > 0 {
		listenPort = *port
	} else if envPort := os.Getenv("CRONPLUS_PORT"); envPort != "" {
		fmt.Sscanf(envPort, "%d", &listenPort)
	}

	// Config paths
	configDir, err := defaultConfigDir()
	if err != nil {
		log.Fatalf("[CronPlus] Failed to resolve config dir: %v", err)
	}
	tokenPath := filepath.Join(configDir, "auth-token")
	statePath := filepath.Join(configDir, "state.json")

	daemonLock, err := acquireDaemonLock(filepath.Join(configDir, "daemon.lock"), listenPort)
	if err != nil {
		log.Fatalf("[CronPlus] %v", err)
	}
	defer daemonLock.Release()

	// Load or create auth token (stable across upgrades)
	token, err := api.LoadOrCreateToken(tokenPath)
	if err != nil {
		log.Fatalf("[CronPlus] Failed to initialize auth token: %v", err)
	}

	// Initialize delivery service with Telegram driver
	telegramDriver := delivery.NewTelegramDriver()
	deliverySvc := delivery.NewService(telegramDriver)

	// Initialize store and engine
	s := store.New(statePath)
	engine := core.NewEngine(s, deliverySvc)
	engine.SetSettings(store.Settings{WebServerPort: listenPort, WebServerBind: "127.0.0.1"})
	if envMaxRuns := os.Getenv("CRONPLUS_MAX_CONCURRENT_RUNS"); envMaxRuns != "" {
		var maxRuns int
		if _, err := fmt.Sscanf(envMaxRuns, "%d", &maxRuns); err == nil && maxRuns > 0 {
			engine.SetMaxConcurrentRuns(maxRuns)
		}
	}

	// Start scheduler
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	scheduler := core.NewScheduler(engine)
	engine.SetScheduler(scheduler)

	// Restore persisted state
	if err := engine.RestoreState(); err != nil {
		log.Printf("[CronPlus] Warning: failed to restore state: %v", err)
	}
	engine.SetSettings(store.Settings{WebServerPort: listenPort, WebServerBind: "127.0.0.1"})

	go scheduler.Start(ctx)

	// Initialize inbound command system
	commandRouter := inbound.NewRouter(inbound.CommandContext{
		GetTasks:      engine.Tasks,
		GetRunHistory: engine.RunHistory,
		TriggerRun:    engine.StartTaskRun,
		SetEnabled:    engine.SetTaskEnabled,
		NextRunTime:   engine.NextRunTime,
	})

	poller := inbound.NewPoller(
		telegramDriver,
		commandRouter,
		engine.DeliveryProfiles,
		func(record models.CommandRecord) {
			engine.AddCommandRecord(record)
			engine.PersistState()
		},
	)
	engine.OnDeliveryProfilesChanged = poller.Restart

	// Start inbound poller if any profile has commands enabled
	poller.Restart()

	// Prepare embedded web UI filesystem
	webFS, err := fs.Sub(webContent, "web")
	if err != nil {
		log.Fatalf("[CronPlus] Failed to load embedded web UI: %v", err)
	}

	// Start HTTP server
	addr := fmt.Sprintf("127.0.0.1:%d", listenPort)
	server := api.NewServerWithInfo(engine, token, addr, api.ServerInfo{
		Version:           version,
		Addr:              addr,
		ConfigDir:         configDir,
		TokenPath:         tokenPath,
		StatePath:         statePath,
		MaxConcurrentRuns: engine.MaxConcurrentRuns(),
	})
	httpServer := server.Build(http.FS(webFS))

	// Handle graceful shutdown
	go func() {
		sigCh := make(chan os.Signal, 1)
		signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
		<-sigCh
		log.Println("[CronPlus] Shutting down...")
		cancel()
		poller.Stop()
		engine.TerminateActiveRuns("daemon shutdown")

		shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer shutdownCancel()
		httpServer.Shutdown(shutdownCtx)

		if err := engine.PersistState(); err != nil {
			log.Printf("[CronPlus] Warning: failed to persist state on shutdown: %v", err)
		}
	}()

	if err := httpServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		log.Fatalf("[CronPlus] Server error: %v", err)
	}
}

func runCLICommand(args []string) (bool, int) {
	if len(args) == 0 {
		return false, 0
	}

	switch args[0] {
	case "validate":
		if len(args) != 2 {
			fmt.Fprintln(os.Stderr, "usage: cronplus validate /path/to/task-package")
			return true, 2
		}
		return true, cliValidate(args[1])
	case "check":
		if len(args) != 2 {
			fmt.Fprintln(os.Stderr, "usage: cronplus check /path/to/task-package")
			return true, 2
		}
		return true, cliCheck(args[1])
	case "schema":
		data, err := webContent.ReadFile("schemas/manifest.schema.json")
		if err != nil {
			fmt.Fprintf(os.Stderr, "failed to read embedded schema: %v\n", err)
			return true, 1
		}
		fmt.Print(string(data))
		return true, 0
	case "autostart":
		return true, cliAutostart(args[1:])
	case "status", "list":
		return true, cliAPI(args[0], "", nil)
	case "import":
		if len(args) != 2 {
			fmt.Fprintln(os.Stderr, "usage: cronplus import /path/to/task-package")
			return true, 2
		}
		return true, cliAPI("import", args[1], nil)
	case "reload", "run":
		if len(args) != 2 {
			fmt.Fprintf(os.Stderr, "usage: cronplus %s task-id\n", args[0])
			return true, 2
		}
		return true, cliAPI(args[0], args[1], nil)
	default:
		return false, 0
	}
}

func cliValidate(dir string) int {
	manifestPath, err := manifest.FindManifest(dir)
	if err != nil {
		fmt.Fprintf(os.Stderr, "manifest: %v\n", err)
		return 1
	}
	result, err := manifest.Load(manifestPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "manifest: %v\n", err)
		return 1
	}
	if len(result.Issues) == 0 {
		fmt.Printf("OK %s\n", manifestPath)
		return 0
	}
	for _, issue := range result.Issues {
		fmt.Printf("%s %s: %s\n", issue.Severity, issue.Path, issue.Message)
	}
	if result.HasErrors() {
		return 1
	}
	return 0
}

func cliCheck(dir string) int {
	manifestPath, err := manifest.FindManifest(dir)
	if err != nil {
		fmt.Fprintf(os.Stderr, "manifest: %v\n", err)
		return 1
	}
	result, err := manifest.Load(manifestPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "manifest: %v\n", err)
		return 1
	}
	for _, issue := range result.Issues {
		fmt.Printf("%s %s: %s\n", issue.Severity, issue.Path, issue.Message)
	}
	if result.HasErrors() {
		return 1
	}

	m := result.Manifest
	manifestDir := filepath.Dir(manifestPath)
	if err := core.EnsureEnvironment(m, manifestDir); err != nil {
		fmt.Fprintf(os.Stderr, "environment: %v\n", err)
		return 1
	}

	outcome := core.RunScript(m, manifestDir)
	fmt.Printf("run exit_code=%d duration_ms=%d timed_out=%v\n", outcome.ExitCode, outcome.DurationMs, outcome.TimedOut)
	fmt.Printf("python=%s\nscript=%s\ncwd=%s\n", outcome.Diagnostics.PythonExecutable, outcome.Diagnostics.ScriptPath, outcome.Diagnostics.WorkingDirectory)
	if outcome.Diagnostics.RunDirectory != "" {
		fmt.Printf("run_dir=%s isolated=%v\n", outcome.Diagnostics.RunDirectory, outcome.Diagnostics.IsolatedRun)
	}
	if outcome.Diagnostics.OutputBytesDiscarded > 0 {
		fmt.Printf("output_discarded_bytes=%d\n", outcome.Diagnostics.OutputBytesDiscarded)
	}
	if outcome.Diagnostics.Cleanup.ProcessGroupTerminated || outcome.Diagnostics.Cleanup.DetachedProcessesKilled > 0 {
		fmt.Printf("cleanup process_group_terminated=%v force_killed=%v detached_killed=%d run_dir_removed=%v\n",
			outcome.Diagnostics.Cleanup.ProcessGroupTerminated,
			outcome.Diagnostics.Cleanup.ProcessGroupForceKilled,
			outcome.Diagnostics.Cleanup.DetachedProcessesKilled,
			outcome.Diagnostics.Cleanup.RunDirectoryRemoved,
		)
	}
	if outcome.ParsedResult != nil {
		fmt.Printf("result status=%s summary=%q\n", models.RunStatusFromOutcome(*outcome), outcome.ParsedResult.Summary)
	} else {
		fmt.Println("result missing")
		if m.ResultContract.ExpectStructuredResult {
			return 1
		}
	}
	if outcome.ExitCode != 0 || outcome.TimedOut {
		if outcome.Stderr != "" {
			fmt.Fprintf(os.Stderr, "%s\n", outcome.Stderr)
		}
		return 1
	}
	return 0
}

func cliAPI(command, arg string, body any) int {
	baseURL, err := cliAPIBaseURL()
	if err != nil {
		fmt.Fprintf(os.Stderr, "config: %v\n", err)
		return 1
	}
	token, err := readDefaultToken()
	if err != nil {
		fmt.Fprintf(os.Stderr, "auth: %v\n", err)
		return 1
	}

	method := http.MethodGet
	path := "/api/status"
	switch command {
	case "list":
		path = "/api/tasks"
	case "import":
		method = http.MethodPost
		path = "/api/tasks/import"
		body = map[string]string{"path": arg}
	case "reload":
		method = http.MethodPost
		path = "/api/tasks/" + arg + "/reload"
	case "run":
		method = http.MethodPost
		path = "/api/tasks/" + arg + "/run"
	}

	var reader io.Reader
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			fmt.Fprintf(os.Stderr, "request: %v\n", err)
			return 1
		}
		reader = bytes.NewReader(data)
	}
	req, err := http.NewRequest(method, baseURL+path, reader)
	if err != nil {
		fmt.Fprintf(os.Stderr, "request: %v\n", err)
		return 1
	}
	req.Header.Set("Authorization", "Bearer "+token)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		fmt.Fprintf(os.Stderr, "request: %v\n", err)
		return 1
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	var pretty bytes.Buffer
	if json.Indent(&pretty, data, "", "  ") == nil {
		fmt.Println(pretty.String())
	} else {
		fmt.Println(string(data))
	}
	if resp.StatusCode >= 400 {
		return 1
	}
	return 0
}

func cliAPIBaseURL() (string, error) {
	if port := strings.TrimSpace(os.Getenv("CRONPLUS_PORT")); port != "" {
		return "http://127.0.0.1:" + port, nil
	}

	configDir, err := defaultConfigDir()
	if err != nil {
		return "", err
	}
	if port, err := readDaemonLockPort(filepath.Join(configDir, "daemon.lock")); err == nil && port > 0 {
		return fmt.Sprintf("http://127.0.0.1:%d", port), nil
	}
	return "http://127.0.0.1:9876", nil
}

func defaultConfigDir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".config", "cronplus"), nil
}

func readDefaultToken() (string, error) {
	configDir, err := defaultConfigDir()
	if err != nil {
		return "", err
	}
	data, err := os.ReadFile(filepath.Join(configDir, "auth-token"))
	if err != nil {
		return "", err
	}
	return string(bytes.TrimSpace(data)), nil
}
