package core

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/ran-su/cronplus/internal/models"
)

// ParseResult extracts a structured result from stdout.
// It looks for the last line starting with the given prefix and unmarshals the JSON.
func ParseResult(stdout, prefix string) *models.ParsedResult {
	if prefix == "" {
		prefix = "CRONPLUS_RESULT="
	}

	lines := strings.Split(stdout, "\n")

	// Scan from the end for the result line
	for i := len(lines) - 1; i >= 0; i-- {
		line := strings.TrimSpace(lines[i])
		if strings.HasPrefix(line, prefix) {
			jsonStr := line[len(prefix):]
			var result models.ParsedResult
			if err := json.Unmarshal([]byte(jsonStr), &result); err != nil {
				return nil
			}
			normalizeParsedResultStatus(&result)
			return &result
		}
	}

	return nil
}

func normalizeParsedResultStatus(result *models.ParsedResult) {
	if result == nil || result.Status == "" {
		return
	}

	original := result.Status
	normalized := models.NormalizeRunStatus(original)
	if models.IsValidRunStatus(normalized) {
		result.Status = normalized
		return
	}

	result.Status = "failure"
	if result.Summary == "" {
		result.Summary = fmt.Sprintf("Invalid structured result status: %s", original)
		return
	}
	result.Summary = fmt.Sprintf("Invalid structured result status %q. %s", original, result.Summary)
}
