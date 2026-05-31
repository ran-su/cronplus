package manifest

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestFindManifest(t *testing.T) {
	dir := t.TempDir()
	manifestPath := filepath.Join(dir, "test.cronplus.yaml")
	os.WriteFile(manifestPath, []byte("manifest_version: 1\n"), 0644)

	found, err := FindManifest(dir)
	if err != nil {
		t.Fatalf("FindManifest error: %v", err)
	}
	if found != manifestPath {
		t.Errorf("FindManifest = %q, want %q", found, manifestPath)
	}
}

func TestFindManifest_YML(t *testing.T) {
	dir := t.TempDir()
	manifestPath := filepath.Join(dir, "test.cronplus.yml")
	os.WriteFile(manifestPath, []byte("manifest_version: 1\n"), 0644)

	found, err := FindManifest(dir)
	if err != nil {
		t.Fatalf("FindManifest error: %v", err)
	}
	if found != manifestPath {
		t.Errorf("FindManifest = %q, want %q", found, manifestPath)
	}
}

func TestFindManifest_NotFound(t *testing.T) {
	dir := t.TempDir()
	_, err := FindManifest(dir)
	if err == nil {
		t.Error("FindManifest should return error for empty directory")
	}
}

func TestFindManifest_Multiple(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "a.cronplus.yaml"), []byte("manifest_version: 1\n"), 0644); err != nil {
		t.Fatalf("write manifest a: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "b.cronplus.yml"), []byte("manifest_version: 1\n"), 0644); err != nil {
		t.Fatalf("write manifest b: %v", err)
	}

	_, err := FindManifest(dir)
	if err == nil {
		t.Fatal("FindManifest should reject multiple manifests")
	}
	if !strings.Contains(err.Error(), "multiple .cronplus manifests") {
		t.Fatalf("error = %q, want multiple manifest diagnostic", err)
	}
}

func TestLoad_Valid(t *testing.T) {
	dir := t.TempDir()
	scriptPath := filepath.Join(dir, "script.py")
	os.WriteFile(scriptPath, []byte("print('hello')\n"), 0644)

	manifestContent := `
manifest_version: 1
script:
  path: ./script.py
  name: Test Task
  description: A test task
runtime:
  environment:
    strategy: system
  timeout_seconds: 60
schedule:
  type: cron
  expression: "*/5 * * * *"
`
	manifestPath := filepath.Join(dir, "test.cronplus.yaml")
	os.WriteFile(manifestPath, []byte(manifestContent), 0644)

	result, err := Load(manifestPath)
	if err != nil {
		t.Fatalf("Load error: %v", err)
	}
	if result.HasErrors() {
		for _, issue := range result.Issues {
			t.Errorf("validation issue: [%s] %s: %s", issue.Severity, issue.Path, issue.Message)
		}
		t.Fatal("manifest should not have errors")
	}
	if result.Manifest.Script.Name != "Test Task" {
		t.Errorf("name = %q, want %q", result.Manifest.Script.Name, "Test Task")
	}
}

func TestLoad_MissingScript(t *testing.T) {
	dir := t.TempDir()
	manifestContent := `
manifest_version: 1
script:
  path: ./nonexistent.py
  name: Bad Task
schedule:
  expression: "0 * * * *"
`
	manifestPath := filepath.Join(dir, "test.cronplus.yaml")
	os.WriteFile(manifestPath, []byte(manifestContent), 0644)

	result, err := Load(manifestPath)
	if err != nil {
		t.Fatalf("Load error: %v", err)
	}
	if !result.HasErrors() {
		t.Error("should have errors for missing script file")
	}
}

func TestLoad_InvalidSchedule(t *testing.T) {
	dir := t.TempDir()
	scriptPath := filepath.Join(dir, "script.py")
	os.WriteFile(scriptPath, []byte("pass\n"), 0644)

	manifestContent := `
manifest_version: 1
script:
  path: ./script.py
  name: Bad Schedule
schedule:
  expression: "60 * * * *"
  timezone: Not/AZone
`
	manifestPath := filepath.Join(dir, "test.cronplus.yaml")
	os.WriteFile(manifestPath, []byte(manifestContent), 0644)

	result, err := Load(manifestPath)
	if err != nil {
		t.Fatalf("Load error: %v", err)
	}
	if !result.HasErrors() {
		t.Fatal("should have errors for invalid schedule")
	}
	assertIssuePath(t, result.Issues, "schedule.expression")
	assertIssuePath(t, result.Issues, "schedule.timezone")
}

func TestLoad_InvalidCronStepTooLarge(t *testing.T) {
	dir := t.TempDir()
	scriptPath := filepath.Join(dir, "script.py")
	os.WriteFile(scriptPath, []byte("pass\n"), 0644)

	manifestContent := `
manifest_version: 1
script:
  path: ./script.py
  name: Bad Step
schedule:
  expression: "*/100 * * * *"
`
	manifestPath := filepath.Join(dir, "test.cronplus.yaml")
	os.WriteFile(manifestPath, []byte(manifestContent), 0644)

	result, err := Load(manifestPath)
	if err != nil {
		t.Fatalf("Load error: %v", err)
	}
	if !result.HasErrors() {
		t.Fatal("should have errors for oversized cron step")
	}
	assertIssuePath(t, result.Issues, "schedule.expression")
}

func TestLoad_Defaults(t *testing.T) {
	dir := t.TempDir()
	scriptPath := filepath.Join(dir, "script.py")
	os.WriteFile(scriptPath, []byte("pass\n"), 0644)

	manifestContent := `
manifest_version: 1
script:
  path: ./script.py
  name: Defaults Test
schedule:
  expression: "0 * * * *"
`
	manifestPath := filepath.Join(dir, "test.cronplus.yaml")
	os.WriteFile(manifestPath, []byte(manifestContent), 0644)

	result, err := Load(manifestPath)
	if err != nil {
		t.Fatalf("Load error: %v", err)
	}
	m := result.Manifest
	if m.Runtime.TimeoutSeconds != 120 {
		t.Errorf("default timeout = %d, want 120", m.Runtime.TimeoutSeconds)
	}
	if m.Runtime.MaxOutputKB != 512 {
		t.Errorf("default maxOutputKB = %d, want 512", m.Runtime.MaxOutputKB)
	}
	if !m.RunIsolationEnabled() {
		t.Error("run isolation should be enabled by default")
	}
	if m.Runtime.ResourceLimits.GracefulKillSeconds != 5 {
		t.Errorf("default graceful kill = %d, want 5", m.Runtime.ResourceLimits.GracefulKillSeconds)
	}
	if m.Schedule.Timezone != "UTC" {
		t.Errorf("default timezone = %q, want UTC", m.Schedule.Timezone)
	}
	if m.Schedule.MissedRunPolicy != "skip" {
		t.Errorf("default missed_run_policy = %q, want skip", m.Schedule.MissedRunPolicy)
	}
	if m.ResultContract.ResultPrefix != "CRONPLUS_RESULT=" {
		t.Errorf("default prefix = %q", m.ResultContract.ResultPrefix)
	}
}

func TestLoad_RuntimeEnvFileAndSecretReference(t *testing.T) {
	dir := t.TempDir()
	scriptPath := filepath.Join(dir, "script.py")
	os.WriteFile(scriptPath, []byte("pass\n"), 0644)
	os.WriteFile(filepath.Join(dir, ".env"), []byte("TOKEN=file-token\n"), 0600)

	manifestContent := `
manifest_version: 1
script:
  path: ./script.py
  name: Env Test
runtime:
  env_file: ./.env
  env:
    API_TOKEN:
      type: secret
      value: env://CRONPLUS_API_TOKEN
schedule:
  expression: "0 * * * *"
`
	manifestPath := filepath.Join(dir, "test.cronplus.yaml")
	os.WriteFile(manifestPath, []byte(manifestContent), 0644)

	result, err := Load(manifestPath)
	if err != nil {
		t.Fatalf("Load error: %v", err)
	}
	if result.HasErrors() {
		t.Fatalf("manifest should not have errors: %+v", result.Issues)
	}
	if result.Manifest.Runtime.EnvFile != "./.env" {
		t.Errorf("env_file = %q, want ./.env", result.Manifest.Runtime.EnvFile)
	}
}

func TestLoad_InvalidMissedRunPolicy(t *testing.T) {
	dir := t.TempDir()
	scriptPath := filepath.Join(dir, "script.py")
	os.WriteFile(scriptPath, []byte("pass\n"), 0644)

	manifestContent := `
manifest_version: 1
script:
  path: ./script.py
  name: Backfill Test
schedule:
  expression: "0 * * * *"
  missed_run_policy: backfill
`
	manifestPath := filepath.Join(dir, "test.cronplus.yaml")
	os.WriteFile(manifestPath, []byte(manifestContent), 0644)

	result, err := Load(manifestPath)
	if err != nil {
		t.Fatalf("Load error: %v", err)
	}
	if !result.HasErrors() {
		t.Fatal("should have errors for unsupported missed_run_policy")
	}
	assertIssuePath(t, result.Issues, "schedule.missed_run_policy")
}

func TestLoad_InvalidResourceLimit(t *testing.T) {
	dir := t.TempDir()
	scriptPath := filepath.Join(dir, "script.py")
	os.WriteFile(scriptPath, []byte("pass\n"), 0644)

	manifestContent := `
manifest_version: 1
script:
  path: ./script.py
  name: Limit Test
runtime:
  resource_limits:
    graceful_kill_seconds: -1
schedule:
  expression: "0 * * * *"
`
	manifestPath := filepath.Join(dir, "test.cronplus.yaml")
	os.WriteFile(manifestPath, []byte(manifestContent), 0644)

	result, err := Load(manifestPath)
	if err != nil {
		t.Fatalf("Load error: %v", err)
	}
	if !result.HasErrors() {
		t.Fatal("should have errors for invalid resource limit")
	}
	assertIssuePath(t, result.Issues, "runtime.resource_limits.graceful_kill_seconds")
}

func assertIssuePath(t *testing.T, issues []ValidationIssue, path string) {
	t.Helper()
	for _, issue := range issues {
		if issue.Path == path {
			return
		}
	}
	t.Fatalf("missing issue path %q in %+v", path, issues)
}
