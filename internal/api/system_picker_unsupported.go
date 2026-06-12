//go:build !darwin

package api

import "context"

func pickDirectory(ctx context.Context) (directoryPickerResult, error) {
	return directoryPickerResult{}, errDirectoryPickerUnavailable
}
