package store

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
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
	WebServerPort int    `json:"webServerPort"`
	WebServerBind string `json:"webServerBind"`
}

// Store manages JSON-file persistence of app state.
type Store struct {
	mu   sync.Mutex
	path string
}

// New creates a store backed by a JSON file.
// Defaults to ~/.config/cronplus/state.json.
func New(path string) *Store {
	if path == "" {
		home, _ := os.UserHomeDir()
		path = filepath.Join(home, ".config", "cronplus", "state.json")
	}
	return &Store{path: path}
}

// Path returns the file path for the state store.
func (s *Store) Path() string {
	return s.path
}

// Load reads the persisted state from disk.
// Returns an empty state if the file doesn't exist.
func (s *Store) Load() (*State, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	data, err := os.ReadFile(s.path)
	if os.IsNotExist(err) {
		return s.defaultState(), nil
	}
	if err != nil {
		return nil, fmt.Errorf("failed to read state: %w", err)
	}

	var state State
	if err := json.Unmarshal(data, &state); err != nil {
		return nil, fmt.Errorf("failed to parse state: %w", err)
	}

	if state.RunHistory == nil {
		state.RunHistory = make(map[string][]models.RunRecord)
	}
	if state.Settings.WebServerPort == 0 {
		state.Settings.WebServerPort = 9876
	}
	if state.Settings.WebServerBind == "" {
		state.Settings.WebServerBind = "127.0.0.1"
	}

	return &state, nil
}

// Save writes the state to disk atomically.
func (s *Store) Save(state *State) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	dir := filepath.Dir(s.path)
	if err := os.MkdirAll(dir, 0700); err != nil {
		return fmt.Errorf("failed to create config dir: %w", err)
	}

	data, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal state: %w", err)
	}

	// Atomic write: write to temp file, then rename
	tmpPath := s.path + ".tmp"
	if err := os.WriteFile(tmpPath, data, 0600); err != nil {
		return fmt.Errorf("failed to write state: %w", err)
	}

	if err := os.Rename(tmpPath, s.path); err != nil {
		os.Remove(tmpPath)
		return fmt.Errorf("failed to commit state: %w", err)
	}

	return nil
}

func (s *Store) defaultState() *State {
	return &State{
		RunHistory: make(map[string][]models.RunRecord),
		Settings: Settings{
			WebServerPort: 9876,
			WebServerBind: "127.0.0.1",
		},
	}
}
