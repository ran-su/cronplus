package models

import "time"

type OperationalDiagnostics struct {
	GeneratedAt      time.Time              `json:"generatedAt"`
	ProcessStartedAt time.Time              `json:"processStartedAt"`
	UptimeMs         int64                  `json:"uptimeMs"`
	Scheduler        SchedulerDiagnostics   `json:"scheduler"`
	Persistence      PersistenceDiagnostics `json:"persistence"`
	Delivery         DeliveryDiagnostics    `json:"delivery"`
	DaemonStarts     []time.Time            `json:"daemonStarts"`
	Restarts24h      int                    `json:"restarts24h"`
}

type SchedulerDiagnostics struct {
	Ticks             int64                  `json:"ticks"`
	LastTickAt        *time.Time             `json:"lastTickAt,omitempty"`
	LastLagMs         int64                  `json:"lastLagMs"`
	MaxLagMs          int64                  `json:"maxLagMs"`
	QueueDepth        int                    `json:"queueDepth"`
	QueueSupported    bool                   `json:"queueSupported"`
	ActiveRuns        int                    `json:"activeRuns"`
	MaxConcurrentRuns int                    `json:"maxConcurrentRuns"`
	SkippedTotal      int64                  `json:"skippedTotal"`
	RecentSkipped     []ScheduledRunDecision `json:"recentSkipped"`
}

type ScheduledRunDecision struct {
	OccurredAt  time.Time `json:"occurredAt"`
	ScheduledAt time.Time `json:"scheduledAt"`
	TaskID      string    `json:"taskID"`
	TaskName    string    `json:"taskName"`
	Reason      string    `json:"reason"`
	Message     string    `json:"message"`
}

type PersistenceDiagnostics struct {
	Status         string     `json:"status"`
	Attempts       int64      `json:"attempts"`
	Successes      int64      `json:"successes"`
	Failures       int64      `json:"failures"`
	LastAttemptAt  *time.Time `json:"lastAttemptAt,omitempty"`
	LastSuccessAt  *time.Time `json:"lastSuccessAt,omitempty"`
	LastFailureAt  *time.Time `json:"lastFailureAt,omitempty"`
	LastDurationMs int64      `json:"lastDurationMs"`
	LastError      string     `json:"lastError,omitempty"`
}

type DeliveryDiagnostics struct {
	Attempts         int64             `json:"attempts"`
	Successes        int64             `json:"successes"`
	Failures         int64             `json:"failures"`
	LastLatencyMs    int64             `json:"lastLatencyMs"`
	AverageLatencyMs int64             `json:"averageLatencyMs"`
	MaxLatencyMs     int64             `json:"maxLatencyMs"`
	RecentAttempts   []DeliveryAttempt `json:"recentAttempts"`
}

type DeliveryAttempt struct {
	OccurredAt  time.Time `json:"occurredAt"`
	TaskID      string    `json:"taskID"`
	TaskName    string    `json:"taskName"`
	RunID       string    `json:"runID"`
	ProfileID   string    `json:"profileID"`
	ProfileName string    `json:"profileName"`
	DriverType  string    `json:"driverType"`
	Status      string    `json:"status"`
	LatencyMs   int64     `json:"latencyMs"`
	Error       string    `json:"error,omitempty"`
}
