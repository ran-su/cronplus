package main

import (
	"bytes"
	"encoding/xml"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
)

const (
	launchAgentLabel    = "com.cronplus.daemon"
	launchAgentFileName = launchAgentLabel + ".plist"
)

type launchAgentOptions struct {
	Label       string
	BinaryPath  string
	HomeDir     string
	LogPath     string
	Port        int
	Environment map[string]string
}

func cliAutostart(args []string) int {
	if len(args) == 0 {
		printAutostartUsage(os.Stderr)
		return 2
	}

	switch args[0] {
	case "install":
		return cliAutostartInstall(args[1:])
	case "uninstall", "remove":
		return cliAutostartUninstall(args[1:])
	case "status":
		return cliAutostartStatus(args[1:])
	case "help", "-h", "--help":
		printAutostartUsage(os.Stdout)
		return 0
	default:
		fmt.Fprintf(os.Stderr, "unknown autostart command: %s\n", args[0])
		printAutostartUsage(os.Stderr)
		return 2
	}
}

func printAutostartUsage(out *os.File) {
	fmt.Fprintln(out, "usage:")
	fmt.Fprintln(out, "  cronplus autostart install [--path /path/to/cronplus] [--port 9876] [--max-concurrent-runs N] [--no-start]")
	fmt.Fprintln(out, "  cronplus autostart status")
	fmt.Fprintln(out, "  cronplus autostart uninstall")
}

func cliAutostartInstall(args []string) int {
	if err := requireDarwinAutostart(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}

	flags := flag.NewFlagSet("cronplus autostart install", flag.ContinueOnError)
	flags.SetOutput(os.Stderr)
	binaryPath := flags.String("path", "", "binary path to launch")
	port := flags.Int("port", 0, "HTTP port to pass to cronplus")
	maxRuns := flags.Int("max-concurrent-runs", 0, "CRONPLUS_MAX_CONCURRENT_RUNS value")
	noStart := flags.Bool("no-start", false, "install for the next login without loading now")
	if err := flags.Parse(args); err != nil {
		if err == flag.ErrHelp {
			return 0
		}
		return 2
	}
	if flags.NArg() != 0 {
		fmt.Fprintf(os.Stderr, "unexpected argument: %s\n", flags.Arg(0))
		return 2
	}
	if *port < 0 {
		fmt.Fprintln(os.Stderr, "--port must be positive")
		return 2
	}
	if *maxRuns < 0 {
		fmt.Fprintln(os.Stderr, "--max-concurrent-runs must be positive")
		return 2
	}

	home, err := os.UserHomeDir()
	if err != nil {
		fmt.Fprintf(os.Stderr, "home directory: %v\n", err)
		return 1
	}

	resolvedBinaryPath := *binaryPath
	if resolvedBinaryPath == "" {
		resolvedBinaryPath, err = defaultExecutablePath()
		if err != nil {
			fmt.Fprintf(os.Stderr, "executable path: %v\n", err)
			return 1
		}
	} else {
		resolvedBinaryPath, err = expandAndAbsPath(resolvedBinaryPath, home)
		if err != nil {
			fmt.Fprintf(os.Stderr, "binary path: %v\n", err)
			return 1
		}
	}
	if err := validateLaunchBinary(resolvedBinaryPath); err != nil {
		fmt.Fprintf(os.Stderr, "binary path: %v\n", err)
		return 1
	}

	selectedPort := *port
	if selectedPort == 0 {
		if envPort := strings.TrimSpace(os.Getenv("CRONPLUS_PORT")); envPort != "" {
			parsedPort, err := strconv.Atoi(envPort)
			if err != nil || parsedPort <= 0 {
				fmt.Fprintf(os.Stderr, "CRONPLUS_PORT must be a positive integer: %q\n", envPort)
				return 2
			}
			selectedPort = parsedPort
		}
	}

	environment := map[string]string{}
	selectedMaxRuns := *maxRuns
	if selectedMaxRuns == 0 {
		if envMaxRuns := strings.TrimSpace(os.Getenv("CRONPLUS_MAX_CONCURRENT_RUNS")); envMaxRuns != "" {
			parsedMaxRuns, err := strconv.Atoi(envMaxRuns)
			if err != nil || parsedMaxRuns <= 0 {
				fmt.Fprintf(os.Stderr, "CRONPLUS_MAX_CONCURRENT_RUNS must be a positive integer: %q\n", envMaxRuns)
				return 2
			}
			selectedMaxRuns = parsedMaxRuns
		}
	}
	if selectedMaxRuns > 0 {
		environment["CRONPLUS_MAX_CONCURRENT_RUNS"] = strconv.Itoa(selectedMaxRuns)
	}

	plistPath := launchAgentPath(home)
	logPath := launchAgentLogPath(home)
	opts := launchAgentOptions{
		Label:       launchAgentLabel,
		BinaryPath:  resolvedBinaryPath,
		HomeDir:     home,
		LogPath:     logPath,
		Port:        selectedPort,
		Environment: environment,
	}
	if err := installLaunchAgent(plistPath, opts); err != nil {
		fmt.Fprintf(os.Stderr, "install autostart: %v\n", err)
		return 1
	}

	if *noStart {
		fmt.Printf("Autostart installed for the next login.\nLaunchAgent: %s\nBinary: %s\n", plistPath, resolvedBinaryPath)
		return 0
	}

	configDir, err := defaultConfigDir()
	if err != nil {
		fmt.Fprintf(os.Stderr, "config: %v\n", err)
		return 1
	}
	daemonRunning, err := daemonLockHeld(filepath.Join(configDir, "daemon.lock"))
	if err != nil {
		fmt.Fprintf(os.Stderr, "daemon lock: %v\n", err)
		return 1
	}
	if daemonRunning {
		fmt.Printf("Autostart installed for the next login.\nCronPlus is already running, so it was not started again.\nLaunchAgent: %s\nBinary: %s\n", plistPath, resolvedBinaryPath)
		if selectedPort > 0 {
			fmt.Printf("Port: %d\n", selectedPort)
		}
		fmt.Printf("Log: %s\n", logPath)
		return 0
	}

	if output, err := reloadLaunchAgent(plistPath); err != nil {
		fmt.Fprintf(os.Stderr, "load autostart: %v\n", err)
		if output != "" {
			fmt.Fprintln(os.Stderr, output)
		}
		return 1
	}

	fmt.Printf("Autostart installed and loaded.\nLaunchAgent: %s\nBinary: %s\n", plistPath, resolvedBinaryPath)
	if selectedPort > 0 {
		fmt.Printf("Port: %d\n", selectedPort)
	}
	fmt.Printf("Log: %s\n", logPath)
	return 0
}

func cliAutostartUninstall(args []string) int {
	if err := requireDarwinAutostart(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	if len(args) != 0 {
		fmt.Fprintf(os.Stderr, "unexpected argument: %s\n", args[0])
		return 2
	}

	home, err := os.UserHomeDir()
	if err != nil {
		fmt.Fprintf(os.Stderr, "home directory: %v\n", err)
		return 1
	}
	plistPath := launchAgentPath(home)
	if output, err := unloadLaunchAgent(); err != nil && !isLaunchctlNotLoaded(output) {
		fmt.Fprintf(os.Stderr, "unload autostart: %v\n", err)
		if output != "" {
			fmt.Fprintln(os.Stderr, output)
		}
		return 1
	}
	if err := os.Remove(plistPath); err != nil && !os.IsNotExist(err) {
		fmt.Fprintf(os.Stderr, "remove LaunchAgent: %v\n", err)
		return 1
	}
	fmt.Printf("Autostart removed.\nLaunchAgent: %s\n", plistPath)
	return 0
}

func cliAutostartStatus(args []string) int {
	if err := requireDarwinAutostart(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	if len(args) != 0 {
		fmt.Fprintf(os.Stderr, "unexpected argument: %s\n", args[0])
		return 2
	}

	home, err := os.UserHomeDir()
	if err != nil {
		fmt.Fprintf(os.Stderr, "home directory: %v\n", err)
		return 1
	}
	plistPath := launchAgentPath(home)
	_, statErr := os.Stat(plistPath)
	output, printErr := printLaunchAgent()
	switch {
	case printErr == nil:
		fmt.Println("Autostart: loaded")
		for _, line := range launchAgentStatusLines(output) {
			fmt.Println(line)
		}
		fmt.Printf("LaunchAgent: %s\n", plistPath)
		return 0
	case os.IsNotExist(statErr):
		fmt.Println("Autostart: not installed")
		fmt.Printf("LaunchAgent: %s\n", plistPath)
		return 1
	case statErr != nil:
		fmt.Fprintf(os.Stderr, "inspect LaunchAgent: %v\n", statErr)
		return 1
	default:
		fmt.Println("Autostart: installed but not loaded")
		fmt.Printf("LaunchAgent: %s\n", plistPath)
		if trimmed := strings.TrimSpace(output); trimmed != "" && !isLaunchctlNotLoaded(trimmed) {
			fmt.Println(trimmed)
		}
		return 1
	}
}

func requireDarwinAutostart() error {
	if runtime.GOOS != "darwin" {
		return fmt.Errorf("autostart is currently supported only on macOS launchd; current platform is %s", runtime.GOOS)
	}
	return nil
}

func defaultExecutablePath() (string, error) {
	executablePath, err := os.Executable()
	if err != nil {
		return "", err
	}
	return filepath.Abs(executablePath)
}

func expandAndAbsPath(path, home string) (string, error) {
	if path == "~" {
		path = home
	} else if strings.HasPrefix(path, "~/") {
		path = filepath.Join(home, strings.TrimPrefix(path, "~/"))
	}
	return filepath.Abs(path)
}

func validateLaunchBinary(path string) error {
	if err := validateExecutableBinary(path); err != nil {
		return err
	}
	resolvedPath := path
	if evaluatedPath, err := filepath.EvalSymlinks(path); err == nil {
		resolvedPath = evaluatedPath
	}
	if isLikelyUnstableExecutablePath(resolvedPath) {
		return fmt.Errorf("looks like a temporary build or installer path; install cronplus to a stable location first and pass that path with --path")
	}
	return nil
}

func validateExecutableBinary(path string) error {
	if !filepath.IsAbs(path) {
		return fmt.Errorf("must be absolute: %s", path)
	}
	resolvedPath := path
	if evaluatedPath, err := filepath.EvalSymlinks(path); err == nil {
		resolvedPath = evaluatedPath
	}
	info, err := os.Stat(resolvedPath)
	if err != nil {
		return err
	}
	if info.IsDir() {
		return fmt.Errorf("is a directory: %s", path)
	}
	if info.Mode().IsRegular() && info.Mode()&0o111 == 0 {
		return fmt.Errorf("is not executable: %s", path)
	}
	script, err := looksLikeLauncherScript(resolvedPath)
	if err != nil {
		return fmt.Errorf("cannot inspect executable: %w", err)
	}
	if script {
		return fmt.Errorf("is a launcher script, not the cronplus binary; install the real binary to a stable path first")
	}
	return nil
}

func isLikelyUnstableExecutablePath(path string) bool {
	cleanPath := filepath.Clean(path)
	slashPath := filepath.ToSlash(cleanPath)
	for _, root := range []string{"/tmp", "/private/tmp", filepath.ToSlash(os.TempDir())} {
		root = strings.TrimRight(filepath.Clean(root), string(filepath.Separator))
		root = filepath.ToSlash(root)
		if pathWithin(slashPath, root) {
			return true
		}
	}
	if strings.HasPrefix(slashPath, "/var/folders/") || strings.HasPrefix(slashPath, "/private/var/folders/") {
		return true
	}
	if strings.Contains(slashPath, "/go-build") || strings.Contains(slashPath, "/.cache/go-build/") {
		return true
	}
	if strings.Contains(slashPath, "/install-github-") && strings.Contains(slashPath, "/work/") {
		return true
	}
	parts := strings.Split(slashPath, "/")
	for i := 0; i < len(parts)-1; i++ {
		if parts[i] == "work" && strings.HasPrefix(parts[i+1], "cronplus-") {
			return true
		}
	}
	return false
}

func pathWithin(path, root string) bool {
	if root == "" || root == "." {
		return false
	}
	return path == root || strings.HasPrefix(path, root+"/")
}

func looksLikeLauncherScript(path string) (bool, error) {
	file, err := os.Open(path)
	if err != nil {
		return false, err
	}
	defer file.Close()
	header := make([]byte, 2)
	n, err := file.Read(header)
	if err != nil && n == 0 {
		return false, err
	}
	return n >= 2 && header[0] == '#' && header[1] == '!', nil
}

func launchAgentPath(home string) string {
	return filepath.Join(home, "Library", "LaunchAgents", launchAgentFileName)
}

func launchAgentLogPath(home string) string {
	return filepath.Join(home, "Library", "Logs", "cronplus.log")
}

func installLaunchAgent(plistPath string, opts launchAgentOptions) error {
	if err := os.MkdirAll(filepath.Dir(plistPath), 0o755); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(opts.LogPath), 0o755); err != nil {
		return err
	}
	return os.WriteFile(plistPath, []byte(renderLaunchAgentPlist(opts)), 0o644)
}

func reloadLaunchAgent(plistPath string) (string, error) {
	_, _ = unloadLaunchAgent()
	if output, err := runLaunchctl("bootstrap", launchAgentDomain(), plistPath); err != nil {
		return output, err
	}
	if output, err := runLaunchctl("enable", launchAgentServiceTarget()); err != nil {
		return output, err
	}
	return runLaunchctl("kickstart", "-k", launchAgentServiceTarget())
}

func unloadLaunchAgent() (string, error) {
	return runLaunchctl("bootout", launchAgentServiceTarget())
}

func printLaunchAgent() (string, error) {
	return runLaunchctl("print", launchAgentServiceTarget())
}

func launchAgentDomain() string {
	return fmt.Sprintf("gui/%d", os.Getuid())
}

func launchAgentServiceTarget() string {
	return launchAgentDomain() + "/" + launchAgentLabel
}

func runLaunchctl(args ...string) (string, error) {
	cmd := exec.Command("launchctl", args...)
	var output bytes.Buffer
	cmd.Stdout = &output
	cmd.Stderr = &output
	err := cmd.Run()
	return strings.TrimSpace(output.String()), err
}

func isLaunchctlNotLoaded(output string) bool {
	normalized := strings.ToLower(output)
	return strings.Contains(normalized, "could not find service") ||
		strings.Contains(normalized, "no such process") ||
		strings.Contains(normalized, "service is not loaded") ||
		strings.Contains(normalized, "not found")
}

func launchAgentStatusLines(output string) []string {
	var lines []string
	for _, line := range strings.Split(output, "\n") {
		line = strings.TrimSpace(line)
		switch {
		case strings.HasPrefix(line, "pid = "):
			lines = append(lines, "PID: "+strings.TrimSpace(strings.TrimPrefix(line, "pid = ")))
		case strings.HasPrefix(line, "state = "):
			lines = append(lines, "State: "+strings.TrimSpace(strings.TrimPrefix(line, "state = ")))
		case strings.HasPrefix(line, "last exit code = "):
			lines = append(lines, "Last exit code: "+strings.TrimSpace(strings.TrimPrefix(line, "last exit code = ")))
		}
	}
	return lines
}

func renderLaunchAgentPlist(opts launchAgentOptions) string {
	label := opts.Label
	if label == "" {
		label = launchAgentLabel
	}

	args := []string{opts.BinaryPath}
	if opts.Port > 0 {
		args = append(args, "--port", strconv.Itoa(opts.Port))
	}

	env := map[string]string{
		"HOME": opts.HomeDir,
		"PATH": "/usr/local/bin:/opt/homebrew/bin:/usr/bin:/bin:/usr/sbin:/sbin",
	}
	for key, value := range opts.Environment {
		if strings.TrimSpace(value) != "" {
			env[key] = value
		}
	}

	var builder strings.Builder
	builder.WriteString("<?xml version=\"1.0\" encoding=\"UTF-8\"?>\n")
	builder.WriteString("<!DOCTYPE plist PUBLIC \"-//Apple//DTD PLIST 1.0//EN\" \"http://www.apple.com/DTDs/PropertyList-1.0.dtd\">\n")
	builder.WriteString("<plist version=\"1.0\">\n")
	builder.WriteString("<dict>\n")
	writePlistKeyString(&builder, "Label", label)
	writePlistKeyArray(&builder, "ProgramArguments", args)
	writePlistKeyString(&builder, "WorkingDirectory", opts.HomeDir)
	writePlistKeyBool(&builder, "RunAtLoad", true)
	writePlistKeyBool(&builder, "KeepAlive", true)
	writePlistKeyString(&builder, "StandardOutPath", opts.LogPath)
	writePlistKeyString(&builder, "StandardErrorPath", opts.LogPath)
	writePlistKeyDict(&builder, "EnvironmentVariables", env)
	builder.WriteString("</dict>\n")
	builder.WriteString("</plist>\n")
	return builder.String()
}

func writePlistKeyString(builder *strings.Builder, key, value string) {
	builder.WriteString("    <key>")
	builder.WriteString(xmlEscape(key))
	builder.WriteString("</key>\n")
	builder.WriteString("    <string>")
	builder.WriteString(xmlEscape(value))
	builder.WriteString("</string>\n\n")
}

func writePlistKeyArray(builder *strings.Builder, key string, values []string) {
	builder.WriteString("    <key>")
	builder.WriteString(xmlEscape(key))
	builder.WriteString("</key>\n")
	builder.WriteString("    <array>\n")
	for _, value := range values {
		builder.WriteString("        <string>")
		builder.WriteString(xmlEscape(value))
		builder.WriteString("</string>\n")
	}
	builder.WriteString("    </array>\n\n")
}

func writePlistKeyBool(builder *strings.Builder, key string, value bool) {
	builder.WriteString("    <key>")
	builder.WriteString(xmlEscape(key))
	builder.WriteString("</key>\n")
	if value {
		builder.WriteString("    <true/>\n\n")
		return
	}
	builder.WriteString("    <false/>\n\n")
}

func writePlistKeyDict(builder *strings.Builder, key string, values map[string]string) {
	builder.WriteString("    <key>")
	builder.WriteString(xmlEscape(key))
	builder.WriteString("</key>\n")
	builder.WriteString("    <dict>\n")
	keys := make([]string, 0, len(values))
	for valueKey := range values {
		keys = append(keys, valueKey)
	}
	sort.Strings(keys)
	for _, valueKey := range keys {
		builder.WriteString("        <key>")
		builder.WriteString(xmlEscape(valueKey))
		builder.WriteString("</key>\n")
		builder.WriteString("        <string>")
		builder.WriteString(xmlEscape(values[valueKey]))
		builder.WriteString("</string>\n")
	}
	builder.WriteString("    </dict>\n")
}

func xmlEscape(value string) string {
	var escaped bytes.Buffer
	_ = xml.EscapeText(&escaped, []byte(value))
	return escaped.String()
}
