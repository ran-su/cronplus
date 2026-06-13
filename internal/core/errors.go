package core

import (
	"errors"
	"fmt"
)

var (
	ErrTaskNotFound              = errors.New("task not found")
	ErrTaskAlreadyRunning        = errors.New("task already running")
	ErrMaxConcurrentRuns         = errors.New("maximum concurrent runs reached")
	ErrTaskNoManifest            = errors.New("task has no valid manifest")
	ErrDeliveryProfileNotFound   = errors.New("delivery profile not found")
	ErrEnvironmentSetupPending   = errors.New("environment setup in progress")
	ErrEnvironmentSetupFailed    = errors.New("environment setup failed")
	ErrEnvironmentNotRebuildable = errors.New("environment is not rebuildable")
)

// ManifestValidationError is returned when a task manifest fails validation.
type ManifestValidationError struct {
	Details string
}

func (e *ManifestValidationError) Error() string {
	return "manifest validation failed:\n" + e.Details
}

func taskNotFoundError(taskID string) error {
	return fmt.Errorf("%w: %s", ErrTaskNotFound, taskID)
}

func deliveryProfileNotFoundError(id string) error {
	return fmt.Errorf("%w: %s", ErrDeliveryProfileNotFound, id)
}

func maxConcurrentRunsError(limit int) error {
	return fmt.Errorf("%w (%d)", ErrMaxConcurrentRuns, limit)
}

func environmentSetupFailedError(message string) error {
	if message == "" {
		return ErrEnvironmentSetupFailed
	}
	return fmt.Errorf("%w: %s", ErrEnvironmentSetupFailed, message)
}
