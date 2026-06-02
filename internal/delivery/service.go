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
	profileLookup := newProfileLookup(profiles)

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
		return skippedDeliveryResults(delivery.Profiles, profileLookup, fmt.Sprintf("status %s is not configured in send_on", status))
	}

	// Build the message
	message, err := s.buildMessage(task, record, status, delivery.MessageTemplate)
	if err != nil {
		log.Printf("[CronPlus] Delivery skipped for %s — %v.", task.DisplayName, err)
		return skippedDeliveryResults(delivery.Profiles, profileLookup, err.Error())
	}
	if strings.TrimSpace(message) == "" {
		log.Printf("[CronPlus] Delivery skipped for %s — empty message.", task.DisplayName)
		return skippedDeliveryResults(delivery.Profiles, profileLookup, "message template rendered empty")
	}

	// Send to each profile
	var results []models.DeliveryResult
	for _, profileID := range delivery.Profiles {
		profile, ok := profileLookup.get(profileID)
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

type profileLookup struct {
	byID      map[string]models.DeliveryProfile
	aliases   map[string]models.DeliveryProfile
	ambiguous map[string]bool
}

func newProfileLookup(profiles []models.DeliveryProfile) profileLookup {
	lookup := profileLookup{
		byID:      make(map[string]models.DeliveryProfile),
		aliases:   make(map[string]models.DeliveryProfile),
		ambiguous: make(map[string]bool),
	}

	for _, profile := range profiles {
		id := strings.TrimSpace(profile.ID)
		if id != "" {
			lookup.byID[id] = profile
		}
		lookup.addAlias(profile.Name, profile)
		lookup.addAlias(models.Slugify(profile.Name), profile)
	}

	return lookup
}

func (p profileLookup) addAlias(alias string, profile models.DeliveryProfile) {
	alias = strings.TrimSpace(alias)
	if alias == "" {
		return
	}
	p.addNormalizedAlias(alias, profile)
	lower := strings.ToLower(alias)
	if lower != alias {
		p.addNormalizedAlias(lower, profile)
	}
}

func (p profileLookup) addNormalizedAlias(alias string, profile models.DeliveryProfile) {
	if p.ambiguous[alias] {
		return
	}
	if existing, ok := p.aliases[alias]; ok && existing.ID != profile.ID {
		delete(p.aliases, alias)
		p.ambiguous[alias] = true
		return
	}
	p.aliases[alias] = profile
}

func (p profileLookup) get(profileID string) (models.DeliveryProfile, bool) {
	profileID = strings.TrimSpace(profileID)
	if profileID == "" {
		return models.DeliveryProfile{}, false
	}
	if profile, ok := p.byID[profileID]; ok {
		return profile, true
	}
	if p.ambiguous[profileID] {
		return models.DeliveryProfile{}, false
	}
	profile, ok := p.aliases[profileID]
	return profile, ok
}

func skippedDeliveryResults(profileIDs []string, lookup profileLookup, reason string) []models.DeliveryResult {
	results := make([]models.DeliveryResult, 0, len(profileIDs))
	for _, profileID := range profileIDs {
		result := models.DeliveryResult{
			ProfileID:   profileID,
			ProfileName: profileID,
			Status:      "skipped",
			Error:       reason,
		}
		if profile, ok := lookup.get(profileID); ok {
			result.ProfileID = profile.ID
			result.ProfileName = profile.Name
		}
		results = append(results, result)
	}
	return results
}

// PreviewMessage renders the message that would be sent for a run without sending it.
func (s *Service) PreviewMessage(task *models.Task, record *models.RunRecord) (string, error) {
	if task.Manifest == nil {
		return "", fmt.Errorf("task has no manifest")
	}
	status := models.RunStatusFromOutcome(record.Outcome)
	return s.buildMessage(task, record, status, task.Manifest.Delivery.MessageTemplate)
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
) (string, error) {
	summary := ""
	body := ""
	var dataObj any
	data := map[string]any{}
	if record.Outcome.ParsedResult != nil {
		for key, value := range record.Outcome.ParsedResult.Fields {
			data[key] = value
		}
		summary = record.Outcome.ParsedResult.Summary
		dataObj = record.Outcome.ParsedResult.Data
		if record.Outcome.ParsedResult.Deliverable != nil {
			body = record.Outcome.ParsedResult.Deliverable.Body
		}
	}

	setTemplateDefault(data, "task", task.DisplayName)
	data["status"] = status
	setTemplateDefault(data, "summary", summary)
	setTemplateDefault(data, "exitcode", record.Outcome.ExitCode)
	setTemplateDefault(data, "duration", float64(record.Outcome.DurationMs)/1000.0)
	setTemplateDefault(data, "stdout", truncateStr(record.Outcome.Stdout, 500))
	setTemplateDefault(data, "stderr", truncateStr(record.Outcome.Stderr, 500))
	setTemplateDefault(data, "body", body)
	setTemplateDefault(data, "data", dataObj)
	setTemplateDefault(data, "TaskName", data["task"])
	setTemplateDefault(data, "Status", data["status"])
	setTemplateDefault(data, "Summary", data["summary"])
	setTemplateDefault(data, "ExitCode", data["exitcode"])
	setTemplateDefault(data, "Duration", data["duration"])
	setTemplateDefault(data, "Stdout", data["stdout"])
	setTemplateDefault(data, "Stderr", data["stderr"])
	setTemplateDefault(data, "Body", data["body"])
	setTemplateDefault(data, "Data", data["data"])
	if deliverable, ok := data["deliverable"]; ok {
		setTemplateDefault(data, "Deliverable", deliverable)
	}

	// Use custom template if provided
	if tmplStr != "" {
		// Preprocess short field templates {{status}}, {{data.price}} -> {{.status}}, {{.data.price}}.
		re := regexp.MustCompile(`{{\s*([a-zA-Z_][a-zA-Z0-9_]*(?:\.[a-zA-Z_][a-zA-Z0-9_]*)*)\s*}}`)
		tmplStr = re.ReplaceAllStringFunc(tmplStr, func(m string) string {
			match := re.FindStringSubmatch(m)
			if len(match) < 2 {
				return m
			}
			key := match[1]
			if templatePathReserved(key) {
				return m
			}
			return "{{." + key + "}}"
		})

		tmpl, err := template.New("msg").Option("missingkey=error").Parse(tmplStr)
		if err != nil {
			return "", fmt.Errorf("message template parse error: %w", err)
		}
		var buf strings.Builder
		if err := tmpl.Execute(&buf, data); err != nil {
			return "", fmt.Errorf("message template render error: %w", err)
		}
		return buf.String(), nil
	}

	return s.defaultMessage(data), nil
}

func setTemplateDefault(data map[string]any, key string, value any) {
	if _, ok := data[key]; ok {
		return
	}
	data[key] = value
}

func templatePathReserved(path string) bool {
	switch path {
	case "if", "else", "end", "range", "with", "define", "template", "block", "break", "continue",
		"nil", "true", "false",
		"and", "call", "html", "index", "slice", "js", "len", "not", "or", "print", "printf", "println",
		"urlquery", "eq", "ne", "lt", "le", "gt", "ge":
		return true
	default:
		return false
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
