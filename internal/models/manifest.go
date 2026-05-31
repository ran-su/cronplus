package models

// ScriptManifest represents the parsed .cronplus.yaml file.
type ScriptManifest struct {
	ManifestVersion int             `yaml:"manifest_version" json:"manifestVersion"`
	Script          ScriptSection   `yaml:"script" json:"script"`
	Runtime         RuntimeSection  `yaml:"runtime" json:"runtime"`
	Schedule        ScheduleSection `yaml:"schedule" json:"schedule"`
	Delivery        DeliverySection `yaml:"delivery" json:"delivery"`
	UI              UISection       `yaml:"ui" json:"ui"`
	ResultContract  ResultContract  `yaml:"result_contract" json:"resultContract"`
}

type ScriptSection struct {
	Path        string `yaml:"path" json:"path"`
	Name        string `yaml:"name" json:"name"`
	Description string `yaml:"description" json:"description"`
}

type RuntimeSection struct {
	Environment    EnvironmentConfig `yaml:"environment" json:"environment"`
	WorkingDir     string            `yaml:"working_directory" json:"workingDirectory"`
	TimeoutSeconds int               `yaml:"timeout_seconds" json:"timeoutSeconds"`
	MaxOutputKB    int               `yaml:"max_output_kb" json:"maxOutputKB"`
	EnvFile        string            `yaml:"env_file" json:"envFile"`
	Env            map[string]EnvVar `yaml:"env" json:"env"`
	IsolatedRun    *bool             `yaml:"isolated_run" json:"isolatedRun,omitempty"`
	ResourceLimits ResourceLimits    `yaml:"resource_limits" json:"resourceLimits"`
}

type EnvironmentConfig struct {
	Strategy          string `yaml:"strategy" json:"strategy"`
	PythonInterpreter string `yaml:"python_base_interpreter" json:"pythonBaseInterpreter"`
	RequirementsFile  string `yaml:"requirements_file" json:"requirementsFile"`
	VenvPath          string `yaml:"venv_path" json:"venvPath"`
}

// EnvVar supports both plain and secret environment variables.
// In YAML:
//
//	MY_VAR:
//	  type: plain
//	  value: hello
type EnvVar struct {
	Type  string `yaml:"type" json:"type"`
	Value string `yaml:"value" json:"value"`
}

type ResourceLimits struct {
	GracefulKillSeconds int `yaml:"graceful_kill_seconds" json:"gracefulKillSeconds"`
	MaxOpenFiles        int `yaml:"max_open_files" json:"maxOpenFiles,omitempty"`
	MaxProcesses        int `yaml:"max_processes" json:"maxProcesses,omitempty"`
	MaxCPUSeconds       int `yaml:"max_cpu_seconds" json:"maxCPUSeconds,omitempty"`
	MaxMemoryMB         int `yaml:"max_memory_mb" json:"maxMemoryMB,omitempty"`
}

func (r ResourceLimits) HasHardLimits() bool {
	return r.MaxOpenFiles > 0 || r.MaxProcesses > 0 || r.MaxCPUSeconds > 0 || r.MaxMemoryMB > 0
}

func (m *ScriptManifest) RunIsolationEnabled() bool {
	return m.Runtime.IsolatedRun == nil || *m.Runtime.IsolatedRun
}

type ScheduleSection struct {
	Type            string `yaml:"type" json:"type"`
	Expression      string `yaml:"expression" json:"expression"`
	Timezone        string `yaml:"timezone" json:"timezone"`
	MissedRunPolicy string `yaml:"missed_run_policy" json:"missedRunPolicy"`
}

type DeliverySection struct {
	Profiles        []string                `yaml:"profiles" json:"profiles"`
	SendOn          []string                `yaml:"send_on" json:"sendOn"`
	MessageTemplate string                  `yaml:"message_template" json:"messageTemplate"`
	InlineProfiles  []InlineDeliveryProfile `yaml:"inline_profiles" json:"inlineProfiles"`
}

type InlineDeliveryProfile struct {
	ID     string            `yaml:"id" json:"id"`
	Name   string            `yaml:"name" json:"name"`
	Driver string            `yaml:"driver" json:"driver"`
	Config map[string]string `yaml:"config" json:"config"`
}

type UISection struct {
	Category string   `yaml:"category" json:"category"`
	Tags     []string `yaml:"tags" json:"tags"`
}

type ResultContract struct {
	Version                int    `yaml:"version" json:"version"`
	ExpectStructuredResult bool   `yaml:"expect_structured_result" json:"expectStructuredResult"`
	ResultPrefix           string `yaml:"result_prefix" json:"resultPrefix"`
}

// Defaults fills in zero-value fields with sensible defaults.
func (m *ScriptManifest) Defaults() {
	if m.ManifestVersion == 0 {
		m.ManifestVersion = 1
	}
	if m.Runtime.TimeoutSeconds == 0 {
		m.Runtime.TimeoutSeconds = 120
	}
	if m.Runtime.MaxOutputKB == 0 {
		m.Runtime.MaxOutputKB = 512
	}
	if m.Runtime.ResourceLimits.GracefulKillSeconds == 0 {
		m.Runtime.ResourceLimits.GracefulKillSeconds = 5
	}
	if m.Runtime.Environment.Strategy == "" {
		m.Runtime.Environment.Strategy = "system"
	}
	if m.Schedule.Type == "" {
		m.Schedule.Type = "cron"
	}
	if m.Schedule.Timezone == "" {
		m.Schedule.Timezone = "UTC"
	}
	if m.Schedule.MissedRunPolicy == "" {
		m.Schedule.MissedRunPolicy = "skip"
	}
	if len(m.Delivery.SendOn) == 0 {
		m.Delivery.SendOn = []string{"success", "failure"}
	}
	if m.ResultContract.ResultPrefix == "" {
		m.ResultContract.ResultPrefix = "CRONPLUS_RESULT="
	}
	if m.Runtime.Env == nil {
		m.Runtime.Env = make(map[string]EnvVar)
	}
}
