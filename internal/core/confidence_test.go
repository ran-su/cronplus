package core

import (
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/ran-su/cronplus/internal/models"
)

func TestNextRunTimesForManifestReturnsUpcomingRuns(t *testing.T) {
	m := &models.ScriptManifest{}
	m.Defaults()
	m.Schedule.Expression = "0 9 * * *"
	m.Schedule.Timezone = "UTC"

	after := time.Date(2026, 6, 1, 8, 30, 0, 0, time.UTC)
	runs := NextRunTimesForManifest(m, 3, after)

	if len(runs) != 3 {
		t.Fatalf("runs len = %d, want 3: %+v", len(runs), runs)
	}
	if got := runs[0].Format(time.RFC3339); got != "2026-06-01T09:00:00Z" {
		t.Fatalf("first run = %s, want 2026-06-01T09:00:00Z", got)
	}
	if got := runs[1].Format(time.RFC3339); got != "2026-06-02T09:00:00Z" {
		t.Fatalf("second run = %s, want 2026-06-02T09:00:00Z", got)
	}
}

func TestDiagnoseOutcomeRequiredStructuredResult(t *testing.T) {
	diagnosis := DiagnoseOutcome(models.RunOutcome{
		ExitCode: 0,
		Diagnostics: models.RunDiagnostics{
			StructuredResultFound: false,
		},
	}, nil, true)

	if diagnosis.Status != "failure" {
		t.Fatalf("status = %q, want failure", diagnosis.Status)
	}
	if !strings.Contains(diagnosis.Summary, "structured result") {
		t.Fatalf("summary = %q, want structured result diagnostic", diagnosis.Summary)
	}
}

func TestCheckTaskPackageRunsOneShotCheck(t *testing.T) {
	python, err := exec.LookPath("python3")
	if err != nil {
		t.Skip("python3 not available")
	}
	dir := writeTaskPackage(t, "print('CRONPLUS_RESULT={\"status\":\"success\",\"summary\":\"ready\"}')\n", python)

	check := CheckTaskPackage(dir)

	if check.Status != "success" {
		t.Fatalf("status = %q, want success; check=%+v", check.Status, check)
	}
	if check.Run == nil || check.Run.Status != "success" {
		t.Fatalf("run check = %+v, want successful run", check.Run)
	}
	if len(check.NextRuns) == 0 {
		t.Fatal("NextRuns is empty, want upcoming schedule preview")
	}
}
