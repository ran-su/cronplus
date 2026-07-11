package core

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/ran-su/cronplus/internal/store"
)

func TestOperationalDiagnosticsTracksSchedulerPersistenceAndRestarts(t *testing.T) {
	engine := NewEngine(store.New(filepath.Join(t.TempDir(), "state.db")), nil)
	now := time.Now()
	engine.RecordDaemonStart(now.Add(-time.Hour))
	engine.RecordDaemonStart(now)
	engine.recordSchedulerTick(now.Add(-25*time.Millisecond), now)
	engine.recordScheduledRunSkipped("task-1", "Task One", now, ErrMaxConcurrentRuns)
	if err := engine.PersistState(); err != nil {
		t.Fatalf("PersistState: %v", err)
	}

	diagnostics := engine.OperationalDiagnostics()
	if diagnostics.Scheduler.Ticks != 1 || diagnostics.Scheduler.LastLagMs < 20 {
		t.Fatalf("scheduler diagnostics = %+v", diagnostics.Scheduler)
	}
	if diagnostics.Scheduler.SkippedTotal != 1 || len(diagnostics.Scheduler.RecentSkipped) != 1 || diagnostics.Scheduler.RecentSkipped[0].Reason != "capacity_reached" {
		t.Fatalf("skipped diagnostics = %+v", diagnostics.Scheduler)
	}
	if diagnostics.Scheduler.QueueSupported || diagnostics.Scheduler.QueueDepth != 0 {
		t.Fatalf("queue diagnostics = %+v, want unsupported empty queue", diagnostics.Scheduler)
	}
	if diagnostics.Persistence.Status != "healthy" || diagnostics.Persistence.Successes != 1 {
		t.Fatalf("persistence diagnostics = %+v", diagnostics.Persistence)
	}
	if diagnostics.Restarts24h != 1 || len(diagnostics.DaemonStarts) != 2 {
		t.Fatalf("restart diagnostics = %+v", diagnostics)
	}
}

func TestOperationalDiagnosticsTracksPersistenceFailure(t *testing.T) {
	dir := t.TempDir()
	blocked := filepath.Join(dir, "not-a-directory")
	if err := os.WriteFile(blocked, []byte("blocked"), 0600); err != nil {
		t.Fatalf("write blocker: %v", err)
	}
	engine := NewEngine(store.New(filepath.Join(blocked, "state.db")), nil)
	if err := engine.PersistState(); err == nil {
		t.Fatal("PersistState error is nil, want failure")
	}
	diagnostics := engine.OperationalDiagnostics().Persistence
	if diagnostics.Status != "error" || diagnostics.Failures != 1 || diagnostics.LastError == "" {
		t.Fatalf("persistence diagnostics = %+v", diagnostics)
	}
}
