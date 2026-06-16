package store

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/ran-su/cronplus/internal/models"
)

func normalizeLegacyJSONState(data []byte) ([]byte, error) {
	var root map[string]json.RawMessage
	if err := json.Unmarshal(data, &root); err != nil {
		return nil, fmt.Errorf("failed to parse legacy JSON state: %w", err)
	}
	if root == nil {
		return nil, fmt.Errorf("failed to parse legacy JSON state: state root must be an object")
	}

	if err := normalizeTaskArray(root); err != nil {
		return nil, err
	}
	if err := normalizeDeliveryProfileArray(root); err != nil {
		return nil, err
	}
	if err := normalizeSettings(root); err != nil {
		return nil, err
	}

	normalizedData, err := json.Marshal(root)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal normalized legacy JSON state: %w", err)
	}
	return normalizedData, nil
}

func (s *Store) loadJSONForImportLocked() (*State, error) {
	data, err := os.ReadFile(s.jsonPath)
	if err != nil {
		return nil, fmt.Errorf("failed to read legacy JSON state: %w", err)
	}

	if err := s.writeLegacyJSONBackupLocked(data); err != nil {
		return nil, err
	}
	data, err = normalizeLegacyJSONState(data)
	if err != nil {
		return nil, err
	}

	state, err := decodeLegacyState(data)
	if err != nil {
		return nil, err
	}
	normalizeState(state)
	return state, nil
}

func decodeLegacyState(data []byte) (*State, error) {
	var raw struct {
		Tasks            []PersistedTask          `json:"tasks"`
		DeliveryProfiles []models.DeliveryProfile `json:"deliveryProfiles"`
		Settings         Settings                 `json:"settings"`
		RunHistory       json.RawMessage          `json:"runHistory"`
		ActiveRuns       json.RawMessage          `json:"activeRuns"`
		CommandLog       json.RawMessage          `json:"commandLog"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, fmt.Errorf("failed to parse legacy JSON state core fields: %w", err)
	}

	state := &State{
		Tasks:            raw.Tasks,
		DeliveryProfiles: raw.DeliveryProfiles,
		RunHistory:       map[string][]models.RunRecord{},
		ActiveRuns:       []models.ActiveRunInfo{},
		CommandLog:       []models.CommandRecord{},
		Settings:         raw.Settings,
	}
	decodeOptionalLegacyField(raw.RunHistory, &state.RunHistory)
	decodeOptionalLegacyField(raw.ActiveRuns, &state.ActiveRuns)
	decodeOptionalLegacyField(raw.CommandLog, &state.CommandLog)
	return state, nil
}

func decodeOptionalLegacyField(raw json.RawMessage, target any) {
	if len(bytes.TrimSpace(raw)) == 0 || isRawNull(raw) {
		return
	}
	_ = json.Unmarshal(raw, target)
}

func normalizeTaskArray(root map[string]json.RawMessage) error {
	return normalizeObjectArray(root, "tasks", func(_ int, task map[string]json.RawMessage) error {
		if missingOrNull(task, "enabled") {
			return setRawField(task, "enabled", true)
		}
		return nil
	})
}

func normalizeDeliveryProfileArray(root map[string]json.RawMessage) error {
	usedIDs := map[string]bool{}
	var profiles []map[string]json.RawMessage

	if err := normalizeObjectArray(root, "deliveryProfiles", func(_ int, profile map[string]json.RawMessage) error {
		profiles = append(profiles, profile)
		if id := strings.TrimSpace(rawString(profile, "id")); id != "" {
			usedIDs[id] = true
		}
		return nil
	}); err != nil {
		return err
	}

	for _, profile := range profiles {
		if strings.TrimSpace(rawString(profile, "id")) == "" {
			id := uniqueProfileID(profileIDBase(profile), usedIDs)
			usedIDs[id] = true
			if err := setRawField(profile, "id", id); err != nil {
				return err
			}
		}
		if missingOrNull(profile, "enabled") {
			if err := setRawField(profile, "enabled", true); err != nil {
				return err
			}
		}
		if strings.TrimSpace(rawString(profile, "driverType")) == "" {
			driverType := strings.TrimSpace(rawString(profile, "type"))
			if driverType == "" {
				driverType = "telegram"
			}
			if err := setRawField(profile, "driverType", driverType); err != nil {
				return err
			}
		}
		if missingOrNull(profile, "config") {
			if err := setRawField(profile, "config", map[string]string{}); err != nil {
				return err
			}
		}
		if missingOrNull(profile, "inboundCommandsEnabled") {
			if err := setRawField(profile, "inboundCommandsEnabled", false); err != nil {
				return err
			}
		}
	}

	if profiles == nil {
		profiles = []map[string]json.RawMessage{}
	}
	return setRawField(root, "deliveryProfiles", profiles)
}

func normalizeSettings(root map[string]json.RawMessage) error {
	settings := map[string]json.RawMessage{}
	if !missingOrNull(root, "settings") {
		var err error
		settings, err = rawObject(root["settings"], "settings")
		if err != nil {
			return err
		}
	}
	if rawInt(settings, "webServerPort") == 0 {
		if err := setRawField(settings, "webServerPort", 9876); err != nil {
			return err
		}
	}
	if strings.TrimSpace(rawString(settings, "webServerBind")) == "" {
		if err := setRawField(settings, "webServerBind", "127.0.0.1"); err != nil {
			return err
		}
	}
	return setRawField(root, "settings", settings)
}

func normalizeObjectArray(root map[string]json.RawMessage, field string, normalize func(int, map[string]json.RawMessage) error) error {
	if missingOrNull(root, field) {
		return setRawField(root, field, []any{})
	}

	var rawItems []json.RawMessage
	if err := json.Unmarshal(root[field], &rawItems); err != nil {
		return fmt.Errorf("%s must be an array: %w", field, err)
	}

	items := make([]map[string]json.RawMessage, len(rawItems))
	for i, rawItem := range rawItems {
		item, err := rawObject(rawItem, fmt.Sprintf("%s[%d]", field, i))
		if err != nil {
			return err
		}
		if err := normalize(i, item); err != nil {
			return err
		}
		items[i] = item
	}
	return setRawField(root, field, items)
}

func rawObject(raw json.RawMessage, label string) (map[string]json.RawMessage, error) {
	var item map[string]json.RawMessage
	if err := json.Unmarshal(raw, &item); err != nil {
		return nil, fmt.Errorf("%s must be an object: %w", label, err)
	}
	if item == nil {
		return nil, fmt.Errorf("%s must be an object", label)
	}
	return item, nil
}

func missingOrNull(object map[string]json.RawMessage, field string) bool {
	raw, ok := object[field]
	return !ok || isRawNull(raw)
}

func isRawNull(raw json.RawMessage) bool {
	return bytes.Equal(bytes.TrimSpace(raw), []byte("null"))
}

func setRawField(object map[string]json.RawMessage, field string, value any) error {
	data, err := json.Marshal(value)
	if err != nil {
		return fmt.Errorf("failed to marshal migrated field %s: %w", field, err)
	}
	object[field] = data
	return nil
}

func rawString(object map[string]json.RawMessage, field string) string {
	raw, ok := object[field]
	if !ok || isRawNull(raw) {
		return ""
	}
	var value string
	if err := json.Unmarshal(raw, &value); err != nil {
		return ""
	}
	return value
}

func rawInt(object map[string]json.RawMessage, field string) int {
	raw, ok := object[field]
	if !ok || isRawNull(raw) {
		return 0
	}
	var value int
	if err := json.Unmarshal(raw, &value); err != nil {
		return 0
	}
	return value
}

func profileIDBase(profile map[string]json.RawMessage) string {
	for _, field := range []string{"name", "driverType", "type"} {
		if base := models.Slugify(rawString(profile, field)); base != "" {
			return base
		}
	}
	return "delivery-profile"
}

func uniqueProfileID(base string, used map[string]bool) string {
	if base == "" {
		base = "delivery-profile"
	}
	id := base
	for i := 2; used[id]; i++ {
		id = base + "-" + strconv.Itoa(i)
	}
	return id
}

func (s *Store) writeLegacyJSONBackupLocked(data []byte) error {
	for i := 0; ; i++ {
		path := s.jsonPath + ".bak"
		if i > 0 {
			path = fmt.Sprintf("%s.%d", path, i)
		}
		file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
		if os.IsExist(err) {
			continue
		}
		if err != nil {
			return fmt.Errorf("failed to create legacy JSON state backup: %w", err)
		}
		if _, err := file.Write(data); err != nil {
			file.Close()
			return fmt.Errorf("failed to write legacy JSON state backup: %w", err)
		}
		if err := file.Close(); err != nil {
			return fmt.Errorf("failed to close legacy JSON state backup: %w", err)
		}
		return nil
	}
}
