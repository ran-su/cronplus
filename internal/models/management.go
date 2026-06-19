package models

import "time"

type DependencyHealthReport struct {
	TaskID       string                 `json:"taskID"`
	TaskName     string                 `json:"taskName"`
	TaskSlug     string                 `json:"taskSlug"`
	Status       string                 `json:"status"`
	Summary      string                 `json:"summary"`
	Dependencies []DependencyHealthItem `json:"dependencies"`
}

type DependencyHealthItem struct {
	Index          int            `json:"index"`
	Selector       string         `json:"selector"`
	Config         TaskDependency `json:"config"`
	RequiredStatus string         `json:"requiredStatus"`
	MaxAgeSeconds  int            `json:"maxAgeSeconds,omitempty"`
	OnUnhealthy    string         `json:"onUnhealthy"`
	Status         string         `json:"status"`
	Reason         string         `json:"reason,omitempty"`
	Ambiguous      bool           `json:"ambiguous,omitempty"`
	TargetID       string         `json:"targetID,omitempty"`
	TargetName     string         `json:"targetName,omitempty"`
	TargetSlug     string         `json:"targetSlug,omitempty"`
	LastRunID      string         `json:"lastRunID,omitempty"`
	LastStatus     string         `json:"lastStatus,omitempty"`
	LastFinishedAt *time.Time     `json:"lastFinishedAt,omitempty"`
	LastAgeSeconds int64          `json:"lastAgeSeconds,omitempty"`
}

type TaskDependentsReport struct {
	TaskID     string          `json:"taskID"`
	TaskName   string          `json:"taskName"`
	TaskSlug   string          `json:"taskSlug"`
	Dependents []TaskDependent `json:"dependents"`
}

type TaskDependent struct {
	TaskID         string `json:"taskID"`
	TaskName       string `json:"taskName"`
	TaskSlug       string `json:"taskSlug"`
	Index          int    `json:"index"`
	Selector       string `json:"selector"`
	RequiredStatus string `json:"requiredStatus"`
	MaxAgeSeconds  int    `json:"maxAgeSeconds,omitempty"`
	OnUnhealthy    string `json:"onUnhealthy"`
}

type DirectoryUsage struct {
	Path        string `json:"path,omitempty"`
	Exists      bool   `json:"exists"`
	Bytes       int64  `json:"bytes"`
	Files       int64  `json:"files"`
	Directories int64  `json:"directories"`
	Error       string `json:"error,omitempty"`
}

type TaskEnvironmentDetail struct {
	TaskID           string                 `json:"taskID"`
	TaskName         string                 `json:"taskName"`
	TaskSlug         string                 `json:"taskSlug"`
	Strategy         string                 `json:"strategy"`
	Setup            EnvironmentSetupStatus `json:"setup"`
	PythonExecutable string                 `json:"pythonExecutable,omitempty"`
	PythonBase       string                 `json:"pythonBase,omitempty"`
	RequirementsFile string                 `json:"requirementsFile,omitempty"`
	EnvFile          string                 `json:"envFile,omitempty"`
	VenvPath         string                 `json:"venvPath,omitempty"`
	Usage            DirectoryUsage         `json:"usage"`
	CanRebuild       bool                   `json:"canRebuild"`
	Running          bool                   `json:"running"`
	Message          string                 `json:"message,omitempty"`
}

type SchedulePreview struct {
	Expression string      `json:"expression"`
	Timezone   string      `json:"timezone"`
	After      time.Time   `json:"after"`
	Count      int         `json:"count"`
	Valid      bool        `json:"valid"`
	Message    string      `json:"message,omitempty"`
	Runs       []time.Time `json:"runs"`
}

type HealthReport struct {
	GeneratedAt    time.Time                `json:"generatedAt"`
	Status         string                   `json:"status"`
	Summary        string                   `json:"summary"`
	Tasks          HealthTaskSummary        `json:"tasks"`
	Runs           HealthRunSummary         `json:"runs"`
	Environments   HealthEnvironmentSummary `json:"environments"`
	Storage        HealthStorageSummary     `json:"storage"`
	Retention      RetentionPolicy          `json:"retention"`
	Browser        BrowserHealthSummary     `json:"browser"`
	ActiveRuns     []ActiveRunInfo          `json:"activeRuns"`
	AttentionItems []map[string]any         `json:"attentionItems"`
}

type HealthTaskSummary struct {
	Total    int `json:"total"`
	Enabled  int `json:"enabled"`
	Disabled int `json:"disabled"`
}

type HealthRunSummary struct {
	Total          int `json:"total"`
	RecentFailures int `json:"recentFailures"`
}

type HealthEnvironmentSummary struct {
	Managed      int   `json:"managed"`
	CustomVenv   int   `json:"customVenv"`
	Pending      int   `json:"pending"`
	Failed       int   `json:"failed"`
	TotalBytes   int64 `json:"totalBytes"`
	UnknownSizes int   `json:"unknownSizes"`
}

type HealthStorageSummary struct {
	StateFile    DirectoryUsage `json:"stateFile"`
	ConfigDir    DirectoryUsage `json:"configDir"`
	TaskPackages DirectoryUsage `json:"taskPackages"`
	Environments DirectoryUsage `json:"environments"`
}

type BrowserHealthSummary struct {
	Tasks                       int             `json:"tasks"`
	ActiveRuns                  int             `json:"activeRuns"`
	RecentFailures              int             `json:"recentFailures"`
	StaleRunDirectories         int             `json:"staleRunDirectories"`
	StaleProfileDirectories     int             `json:"staleProfileDirectories"`
	SuspectedProcesses          int             `json:"suspectedProcesses"`
	ProfileBytes                int64           `json:"profileBytes"`
	DownloadBytes               int64           `json:"downloadBytes"`
	CacheBytes                  int64           `json:"cacheBytes"`
	StaleRunDirectoryUsage      DirectoryUsage  `json:"staleRunDirectoryUsage"`
	StaleProfileDirectoryUsage  DirectoryUsage  `json:"staleProfileDirectoryUsage"`
	ActiveBrowserRuns           []ActiveRunInfo `json:"activeBrowserRuns,omitempty"`
	RecentBrowserFailureTaskIDs []string        `json:"recentBrowserFailureTaskIDs,omitempty"`
	StaleRunDirectoryPaths      []string        `json:"staleRunDirectoryPaths,omitempty"`
	StaleProfileDirectoryPaths  []string        `json:"staleProfileDirectoryPaths,omitempty"`
	BrowserTaskSlugs            []string        `json:"browserTaskSlugs,omitempty"`
}

type RetentionPolicy struct {
	MaxRunsPerTask        int  `json:"maxRunsPerTask"`
	MaxRunAgeDays         int  `json:"maxRunAgeDays"`
	MaxRunOutputKB        int  `json:"maxRunOutputKB"`
	AgePruningEnabled     bool `json:"agePruningEnabled"`
	OutputPruningEnabled  bool `json:"outputPruningEnabled"`
	DefaultMaxRunsPerTask int  `json:"defaultMaxRunsPerTask"`
}

type RetentionCleanupReport struct {
	Policy            RetentionPolicy `json:"policy"`
	RunsBefore        int             `json:"runsBefore"`
	RunsAfter         int             `json:"runsAfter"`
	RunsDeleted       int             `json:"runsDeleted"`
	OutputBytesBefore int64           `json:"outputBytesBefore"`
	OutputBytesAfter  int64           `json:"outputBytesAfter"`
	OutputBytesPruned int64           `json:"outputBytesPruned"`
	TasksAffected     int             `json:"tasksAffected"`
}
