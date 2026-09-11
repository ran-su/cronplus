package core

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/ran-su/cronplus/internal/models"
)

func TestManagedEnvironmentRunsPythonAndInstallsRequirements(t *testing.T) {
	python, err := exec.LookPath(defaultPythonInterpreter())
	if err != nil {
		t.Skip("Python is not installed")
	}
	// Keep this environment integration test entirely offline.
	t.Setenv("PIP_NO_INDEX", "1")
	t.Setenv("PIP_DISABLE_PIP_VERSION_CHECK", "1")
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "requirements.txt"), nil, 0600); err != nil {
		t.Fatal(err)
	}
	script := `import json, sys
print("CRONPLUS_RESULT=" + json.dumps({"status": "success", "summary": "venv ready", "data": {"in_venv": sys.prefix != sys.base_prefix}}))
`
	if err := os.WriteFile(filepath.Join(dir, "script.py"), []byte(script), 0600); err != nil {
		t.Fatal(err)
	}
	m := &models.ScriptManifest{
		Script: models.ScriptSection{Path: "./script.py"},
		Runtime: models.RuntimeSection{
			TimeoutSeconds: 10,
			Environment: models.EnvironmentConfig{
				Strategy: "managed_venv", PythonInterpreter: python, RequirementsFile: "requirements.txt",
			},
		},
	}
	if err := EnsureEnvironment(m, dir); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, ".cronplus-venv", ".cronplus-req-hash")); err != nil {
		t.Fatalf("requirements were not installed: %v", err)
	}
	// An existing environment must be reused even when its base Python moves.
	m.Runtime.Environment.PythonInterpreter = filepath.Join(dir, "missing-python")
	if err := EnsureEnvironment(m, dir); err != nil {
		t.Fatalf("reuse managed environment: %v", err)
	}
	for _, strategy := range []string{"managed_venv", "venv_path"} {
		t.Run(strategy, func(t *testing.T) {
			m.Runtime.Environment.Strategy = strategy
			m.Runtime.Environment.VenvPath = ".cronplus-venv"
			outcome := RunScript(m, dir)
			if outcome.ExitCode != 0 || outcome.ParsedResult == nil {
				t.Fatalf("virtual environment did not run: %+v", outcome)
			}
			data, ok := outcome.ParsedResult.Data.(map[string]any)
			if !ok || data["in_venv"] != true {
				t.Fatalf("script did not use the virtual environment: %+v", outcome.ParsedResult)
			}
		})
	}
}

func TestSystemPythonPreservesConfiguredInterpreter(t *testing.T) {
	m := &models.ScriptManifest{}
	m.Runtime.Environment.Strategy = "system"
	m.Runtime.Environment.PythonInterpreter = filepath.Join(t.TempDir(), "custom-python")
	if got := resolvePython(m, t.TempDir()); got != m.Runtime.Environment.PythonInterpreter {
		t.Fatalf("resolvePython() = %q, want configured interpreter %q", got, m.Runtime.Environment.PythonInterpreter)
	}
}
