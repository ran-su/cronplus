package core

import (
	"errors"
	"time"

	"github.com/ran-su/cronplus/internal/models"
)

const operationalHistoryLimit = 20

type operationalState struct {
	schedulerTicks       int64
	lastSchedulerTickAt  *time.Time
	lastSchedulerLagMs   int64
	maxSchedulerLagMs    int64
	skippedScheduledRuns int64
	recentSkipped        []models.ScheduledRunDecision
	persistence          models.PersistenceDiagnostics
}

func (e *Engine) RecordDaemonStart(startedAt time.Time) {
	if startedAt.IsZero() {
		startedAt = time.Now()
	}
	e.mu.Lock()
	e.daemonStarts = append([]time.Time{startedAt}, e.daemonStarts...)
	if len(e.daemonStarts) > operationalHistoryLimit {
		e.daemonStarts = e.daemonStarts[:operationalHistoryLimit]
	}
	e.mu.Unlock()
}

func (e *Engine) OperationalDiagnostics() models.OperationalDiagnostics {
	now := time.Now()
	e.opsMu.RLock()
	scheduler := models.SchedulerDiagnostics{
		Ticks:          e.operations.schedulerTicks,
		LastTickAt:     cloneTimePtr(e.operations.lastSchedulerTickAt),
		LastLagMs:      e.operations.lastSchedulerLagMs,
		MaxLagMs:       e.operations.maxSchedulerLagMs,
		SkippedTotal:   e.operations.skippedScheduledRuns,
		RecentSkipped:  append([]models.ScheduledRunDecision(nil), e.operations.recentSkipped...),
		QueueSupported: false,
	}
	persistence := e.operations.persistence
	persistence.LastAttemptAt = cloneTimePtr(persistence.LastAttemptAt)
	persistence.LastSuccessAt = cloneTimePtr(persistence.LastSuccessAt)
	persistence.LastFailureAt = cloneTimePtr(persistence.LastFailureAt)
	e.opsMu.RUnlock()

	e.mu.RLock()
	starts := append([]time.Time(nil), e.daemonStarts...)
	scheduler.ActiveRuns = len(e.activeRuns)
	scheduler.MaxConcurrentRuns = e.maxConcurrentRuns
	processStartedAt := e.processStartedAt
	e.mu.RUnlock()

	starts24h := 0
	cutoff := now.Add(-24 * time.Hour)
	for _, startedAt := range starts {
		if startedAt.After(cutoff) {
			starts24h++
		}
	}
	restarts24h := starts24h - 1
	if restarts24h < 0 {
		restarts24h = 0
	}
	deliveryDiagnostics := models.DeliveryDiagnostics{}
	if e.DeliveryService != nil {
		deliveryDiagnostics = e.DeliveryService.Diagnostics()
	}
	return models.OperationalDiagnostics{
		GeneratedAt:      now,
		ProcessStartedAt: processStartedAt,
		UptimeMs:         now.Sub(processStartedAt).Milliseconds(),
		Scheduler:        scheduler,
		Persistence:      persistence,
		Delivery:         deliveryDiagnostics,
		DaemonStarts:     starts,
		Restarts24h:      restarts24h,
	}
}

func (e *Engine) recordSchedulerTick(scheduledAt, observedAt time.Time) {
	lag := observedAt.Sub(scheduledAt).Milliseconds()
	if lag < 0 {
		lag = 0
	}
	e.opsMu.Lock()
	e.operations.schedulerTicks++
	t := observedAt
	e.operations.lastSchedulerTickAt = &t
	e.operations.lastSchedulerLagMs = lag
	if lag > e.operations.maxSchedulerLagMs {
		e.operations.maxSchedulerLagMs = lag
	}
	e.opsMu.Unlock()
}

func (e *Engine) recordScheduledRunSkipped(taskID, taskName string, scheduledAt time.Time, err error) {
	reason := "run_rejected"
	switch {
	case errors.Is(err, ErrTaskAlreadyRunning):
		reason = "already_running"
	case errors.Is(err, ErrMaxConcurrentRuns):
		reason = "capacity_reached"
	case errors.Is(err, ErrEnvironmentSetupPending):
		reason = "environment_pending"
	case errors.Is(err, ErrEnvironmentSetupFailed):
		reason = "environment_failed"
	}
	message := "Scheduled run was skipped."
	if err != nil {
		message = err.Error()
	}
	e.opsMu.Lock()
	e.operations.skippedScheduledRuns++
	e.operations.recentSkipped = append([]models.ScheduledRunDecision{{
		OccurredAt:  time.Now(),
		ScheduledAt: scheduledAt,
		TaskID:      taskID,
		TaskName:    taskName,
		Reason:      reason,
		Message:     message,
	}}, e.operations.recentSkipped...)
	if len(e.operations.recentSkipped) > operationalHistoryLimit {
		e.operations.recentSkipped = e.operations.recentSkipped[:operationalHistoryLimit]
	}
	e.opsMu.Unlock()
}

func (e *Engine) recordPersistenceAttempt(startedAt time.Time, err error) {
	now := time.Now()
	e.opsMu.Lock()
	p := &e.operations.persistence
	p.Attempts++
	p.LastAttemptAt = &now
	p.LastDurationMs = now.Sub(startedAt).Milliseconds()
	if err != nil {
		p.Status = "error"
		p.Failures++
		p.LastFailureAt = &now
		p.LastError = err.Error()
	} else {
		p.Status = "healthy"
		p.Successes++
		p.LastSuccessAt = &now
		p.LastError = ""
	}
	e.opsMu.Unlock()
}

func cloneTimePtr(value *time.Time) *time.Time {
	if value == nil {
		return nil
	}
	copy := *value
	return &copy
}
