package store

import (
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/ran-su/cronplus/internal/models"
)

// State is the top-level persisted state.
type State struct {
	Tasks            []PersistedTask               `json:"tasks"`
	DeliveryProfiles []models.DeliveryProfile      `json:"deliveryProfiles"`
	RunHistory       map[string][]models.RunRecord `json:"runHistory"`
	ActiveRuns       []models.ActiveRunInfo        `json:"activeRuns,omitempty"`
	CommandLog       []models.CommandRecord        `json:"commandLog"`
	Settings         Settings                      `json:"settings"`
}

// PersistedTask stores only the data needed to restore a task on restart.
type PersistedTask struct {
	ID              string    `json:"id"`
	PackageDir      string    `json:"packageDir"`
	Enabled         bool      `json:"enabled"`
	CreatedAt       time.Time `json:"createdAt,omitempty"`
	LastReloadedAt  time.Time `json:"lastReloadedAt,omitempty"`
	ManifestHash    string    `json:"manifestHash,omitempty"`
	ManifestModTime time.Time `json:"manifestModTime,omitempty"`
}

// Settings contains daemon-level configuration.
type Settings struct {
	WebServerPort  int    `json:"webServerPort"`
	WebServerBind  string `json:"webServerBind"`
	MaxRunsPerTask int    `json:"maxRunsPerTask,omitempty"`
	MaxRunAgeDays  int    `json:"maxRunAgeDays,omitempty"`
	MaxRunOutputKB int    `json:"maxRunOutputKB,omitempty"`
}

// Store manages SQLite persistence of app state, with a one-time import path
// from legacy JSON state files.
type Store struct {
	mu       sync.Mutex
	path     string
	dbPath   string
	jsonPath string
}

// New creates a store backed by SQLite. Passing a legacy state.json path stores
// the primary database beside it as state.db and uses the JSON file only as a
// one-time import source.
// Defaults to ~/.config/cronplus/state.db.
func New(path string) *Store {
	if path == "" {
		home, _ := os.UserHomeDir()
		path = filepath.Join(home, ".config", "cronplus", "state.json")
	}
	jsonPath, dbPath := statePaths(path)
	return &Store{path: dbPath, dbPath: dbPath, jsonPath: jsonPath}
}

// Path returns the primary SQLite state file path.
func (s *Store) Path() string {
	return s.path
}

// JSONPath returns the legacy JSON state file path used for one-time imports.
func (s *Store) JSONPath() string {
	return s.jsonPath
}

// Load reads the persisted state from disk.
// Returns an empty state if the file doesn't exist.
func (s *Store) Load() (*State, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	state, err := s.loadSQLiteLocked()
	if err != nil {
		return nil, err
	}

	return state, nil
}

// Save writes the state to disk atomically.
func (s *Store) Save(state *State) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	normalizeState(state)

	return s.saveSQLiteLocked(state)
}

func (s *Store) defaultState() *State {
	return &State{
		Tasks:            []PersistedTask{},
		DeliveryProfiles: []models.DeliveryProfile{},
		RunHistory:       make(map[string][]models.RunRecord),
		ActiveRuns:       []models.ActiveRunInfo{},
		CommandLog:       []models.CommandRecord{},
		Settings: Settings{
			WebServerPort: 9876,
			WebServerBind: "127.0.0.1",
		},
	}
}

func normalizeState(state *State) {
	if state == nil {
		return
	}
	if state.Tasks == nil {
		state.Tasks = []PersistedTask{}
	}
	if state.DeliveryProfiles == nil {
		state.DeliveryProfiles = []models.DeliveryProfile{}
	}
	for i := range state.DeliveryProfiles {
		if state.DeliveryProfiles[i].Config == nil {
			state.DeliveryProfiles[i].Config = map[string]string{}
		}
	}
	if state.RunHistory == nil {
		state.RunHistory = make(map[string][]models.RunRecord)
	}
	if state.ActiveRuns == nil {
		state.ActiveRuns = []models.ActiveRunInfo{}
	}
	if state.CommandLog == nil {
		state.CommandLog = []models.CommandRecord{}
	}
	if state.Settings.WebServerPort == 0 {
		state.Settings.WebServerPort = 9876
	}
	if state.Settings.WebServerBind == "" {
		state.Settings.WebServerBind = "127.0.0.1"
	}
	if state.Settings.MaxRunsPerTask < 0 {
		state.Settings.MaxRunsPerTask = 0
	}
	if state.Settings.MaxRunAgeDays < 0 {
		state.Settings.MaxRunAgeDays = 0
	}
	if state.Settings.MaxRunOutputKB < 0 {
		state.Settings.MaxRunOutputKB = 0
	}
}

func statePaths(path string) (string, string) {
	if path == "" {
		home, _ := os.UserHomeDir()
		path = filepath.Join(home, ".config", "cronplus", "state.json")
	}
	ext := strings.ToLower(filepath.Ext(path))
	switch ext {
	case ".db", ".sqlite", ".sqlite3":
		return strings.TrimSuffix(path, filepath.Ext(path)) + ".json", path
	default:
		return path, strings.TrimSuffix(path, filepath.Ext(path)) + ".db"
	}
}
