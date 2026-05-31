package delivery

import (
	"fmt"
	"log"
	"regexp"
	"strings"
	"text/template"

	"github.com/ran-su/cronplus/internal/models"
)

// Driver is an interface for delivery backends (Telegram, etc.).
type Driver interface {
	Type() string
	Send(profile models.DeliveryProfile, message string) error
}

// Service orchestrates deliveries after a task run.
type Service struct {
	drivers map[string]Driver
}

// NewService creates a delivery service with the given drivers.
func NewService(drivers ...Driver) *Service {
	m := make(map[string]Driver)
	for _, d := range drivers {
		m[d.Type()] = d
	}
	return &Service{drivers: m}
}

// Deliver sends run results to all configured delivery profiles.
// Returns delivery results for each profile.
func (s *Service) Deliver(
	task *models.Task,
	record *models.RunRecord,
	profiles []models.DeliveryProfile,
) []models.DeliveryResult {
	if task.Manifest == nil {
		return nil
	}

	delivery := task.Manifest.Delivery
	if len(delivery.Profiles) == 0 {
		return nil
	}

	status := models.RunStatusFromOutcome(record.Outcome)

	// Check send_on conditions
	shouldSend := false
	for _, condition := range delivery.SendOn {
		if models.NormalizeRunStatus(condition) == status {
			shouldSend = true
			break
		}
	}
	if !shouldSend {
		return nil
	}

	// Build the message
	message := s.buildMessage(task, record, status, delivery.MessageTemplate)
	if strings.TrimSpace(message) == "" {
		log.Printf("[CronPlus] Delivery skipped for %s — empty message.", task.DisplayName)
		return nil
	}

	// Build profile lookup
	profileMap := make(map[string]models.DeliveryProfile)
	for _, p := range profiles {
		profileMap[p.ID] = p
	}

	// Send to each profile
	var results []models.DeliveryResult
	for _, profileID := range delivery.Profiles {
		profile, ok := profileMap[profileID]
		if !ok {
			results = append(results, models.DeliveryResult{
				ProfileID:   profileID,
				ProfileName: profileID,
				Status:      "failed",
				Error:       "profile not found",
			})
			continue
		}

		if !profile.Enabled {
			results = append(results, models.DeliveryResult{
				ProfileID:   profile.ID,
				ProfileName: profile.Name,
				Status:      "skipped",
				Error:       "profile disabled",
			})
			continue
		}

		driver, ok := s.drivers[profile.DriverType]
		if !ok {
			results = append(results, models.DeliveryResult{
				ProfileID:   profile.ID,
				ProfileName: profile.Name,
				Status:      "failed",
				Error:       fmt.Sprintf("unknown driver: %s", profile.DriverType),
			})
			continue
		}

		err := driver.Send(profile, message)
		if err != nil {
			log.Printf("[CronPlus] Delivery failed to %s: %v", profile.Name, err)
			results = append(results, models.DeliveryResult{
				ProfileID:   profile.ID,
				ProfileName: profile.Name,
				Status:      "failed",
				Error:       err.Error(),
			})
		} else {
			log.Printf("[CronPlus] Delivery sent to %s.", profile.Name)
			results = append(results, models.DeliveryResult{
				ProfileID:   profile.ID,
				ProfileName: profile.Name,
				Status:      "success",
			})
		}
	}

	return results
}

// PreviewMessage renders the message that would be sent for a run without sending it.
func (s *Service) PreviewMessage(task *models.Task, record *models.RunRecord) (string, error) {
	if task.Manifest == nil {
		return "", fmt.Errorf("task has no manifest")
	}
	status := models.RunStatusFromOutcome(record.Outcome)
	return s.buildMessage(task, record, status, task.Manifest.Delivery.MessageTemplate), nil
}

// SendTest sends a small test message through a delivery profile.
func (s *Service) SendTest(profile models.DeliveryProfile, message string) error {
	if !profile.Enabled {
		return fmt.Errorf("profile disabled")
	}
	driver, ok := s.drivers[profile.DriverType]
	if !ok {
		return fmt.Errorf("unknown driver: %s", profile.DriverType)
	}
	if strings.TrimSpace(message) == "" {
		message = "CronPlus delivery test"
	}
	return driver.Send(profile, message)
}

func (s *Service) buildMessage(
	task *models.Task,
	record *models.RunRecord,
	status string,
	tmplStr string,
) string {
	summary := ""
	body := ""
	var dataObj any
	if record.Outcome.ParsedResult != nil {
		summary = record.Outcome.ParsedResult.Summary
		dataObj = record.Outcome.ParsedResult.Data
		if record.Outcome.ParsedResult.Deliverable != nil {
			body = record.Outcome.ParsedResult.Deliverable.Body
		}
	}

	data := map[string]any{
		"task":     task.DisplayName,
		"status":   status,
		"summary":  summary,
		"exitcode": record.Outcome.ExitCode,
		"duration": float64(record.Outcome.DurationMs) / 1000.0,
		"stdout":   truncateStr(record.Outcome.Stdout, 500),
		"stderr":   truncateStr(record.Outcome.Stderr, 500),
		"body":     body,
		"data":     dataObj,
	}
	data["TaskName"] = data["task"]
	data["Status"] = data["status"]
	data["Summary"] = data["summary"]
	data["ExitCode"] = data["exitcode"]
	data["Duration"] = data["duration"]
	data["Stdout"] = data["stdout"]
	data["Stderr"] = data["stderr"]
	data["Body"] = data["body"]
	data["Data"] = data["data"]

	// Use custom template if provided
	if tmplStr != "" {
		// Preprocess old V1 style templates {{status}}, {{data.price}} -> {{.status}}, {{.data.price}}
		re := regexp.MustCompile(`{{\s*([a-zA-Z0-9_.]+)\s*}}`)
		tmplStr = re.ReplaceAllStringFunc(tmplStr, func(m string) string {
			match := re.FindStringSubmatch(m)
			if len(match) < 2 {
				return m
			}
			key := match[1]
			if templateKeySupported(key) {
				return "{{." + key + "}}"
			}
			return m
		})

		tmpl, err := template.New("msg").Parse(tmplStr)
		if err != nil {
			log.Printf("[CronPlus] Template parse error: %v. Using default.", err)
			return s.defaultMessage(data)
		}
		var buf strings.Builder
		if err := tmpl.Execute(&buf, data); err != nil {
			log.Printf("[CronPlus] Template exec error: %v. Using default.", err)
			return s.defaultMessage(data)
		}
		return buf.String()
	}

	return s.defaultMessage(data)
}

func templateKeySupported(key string) bool {
	switch key {
	case "task", "status", "summary", "body", "exitcode", "duration", "stdout", "stderr",
		"TaskName", "Status", "Summary", "Body", "ExitCode", "Duration", "Stdout", "Stderr":
		return true
	default:
		return strings.HasPrefix(key, "data.") || strings.HasPrefix(key, "Data.")
	}
}

func (s *Service) defaultMessage(d map[string]any) string {
	icon := "✅"
	if d["status"] != "success" {
		icon = "❌"
	}

	msg := fmt.Sprintf("%s %s — %v", icon, d["task"], d["status"])

	if sStr, ok := d["summary"].(string); ok && sStr != "" {
		msg += "\n" + sStr
	} else if bStr, ok := d["body"].(string); ok && bStr != "" {
		msg += "\n" + bStr
	}

	msg += fmt.Sprintf("\nDuration: %.1fs", d["duration"])

	return msg
}

func truncateStr(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "…"
}
