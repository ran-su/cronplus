package core

import (
	"errors"
	"fmt"
	"testing"
)

func TestTypedErrors(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want error
	}{
		{name: "task not found", err: taskNotFoundError("abc"), want: ErrTaskNotFound},
		{name: "already running", err: fmt.Errorf("%w", ErrTaskAlreadyRunning), want: ErrTaskAlreadyRunning},
		{name: "max concurrent", err: maxConcurrentRunsError(2), want: ErrMaxConcurrentRuns},
		{name: "delivery profile", err: deliveryProfileNotFoundError("p1"), want: ErrDeliveryProfileNotFound},
		{name: "env pending", err: ErrEnvironmentSetupPending, want: ErrEnvironmentSetupPending},
		{name: "env failed", err: environmentSetupFailedError("pip failed"), want: ErrEnvironmentSetupFailed},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if !errors.Is(tc.err, tc.want) {
				t.Fatalf("errors.Is(%v, %v) = false, want true", tc.err, tc.want)
			}
		})
	}

	var manifestErr *ManifestValidationError
	err := &ManifestValidationError{Details: "bad field"}
	if !errors.As(err, &manifestErr) {
		t.Fatal("errors.As should recognize ManifestValidationError")
	}
}
