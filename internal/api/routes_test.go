package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/ran-su/cronplus/internal/core"
	"github.com/ran-su/cronplus/internal/models"
	"github.com/ran-su/cronplus/internal/store"
)

func TestGetTaskRunsUnknownTaskReturns404(t *testing.T) {
	engine := core.NewEngine(store.New(filepath.Join(t.TempDir(), "state.json")), nil)
	mux := http.NewServeMux()
	Routes(mux, engine, "test")

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/tasks/missing/runs", nil)
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusNotFound)
	}
}

func TestCreateDeliveryReturns500WhenPersistFails(t *testing.T) {
	dir := t.TempDir()
	blockedParent := filepath.Join(dir, "not-a-dir")
	if err := os.WriteFile(blockedParent, []byte("blocked"), 0600); err != nil {
		t.Fatalf("write blocked parent: %v", err)
	}

	engine := core.NewEngine(store.New(filepath.Join(blockedParent, "state.json")), nil)
	mux := http.NewServeMux()
	Routes(mux, engine, "test")

	body := []byte(`{"name":"Telegram","driverType":"telegram","enabled":true,"config":{"bot_token":"token","chat_id":"1"}}`)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/deliveries", bytes.NewReader(body))
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want %d; body=%s", rec.Code, http.StatusInternalServerError, rec.Body.String())
	}

	var response map[string]string
	if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if response["error"] != "persist_failed" {
		t.Fatalf("error = %q, want persist_failed", response["error"])
	}
}

func TestCreateDeliveryReturnsNameSlugID(t *testing.T) {
	engine := core.NewEngine(store.New(filepath.Join(t.TempDir(), "state.json")), nil)
	mux := http.NewServeMux()
	Routes(mux, engine, "test")

	body := []byte(`{"name":"My Telegram","driverType":"telegram","enabled":true,"config":{"bot_token":"token","chat_id":"1"}}`)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/deliveries", bytes.NewReader(body))
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, want %d; body=%s", rec.Code, http.StatusCreated, rec.Body.String())
	}

	var response map[string]string
	if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if response["id"] != "my-telegram" {
		t.Fatalf("id = %q, want my-telegram", response["id"])
	}
}

func TestSetDeliveryCommandsEndpoint(t *testing.T) {
	engine := core.NewEngine(store.New(filepath.Join(t.TempDir(), "state.json")), nil)
	id := engine.AddDeliveryProfile(models.DeliveryProfile{
		ID:         "telegram",
		Name:       "Telegram",
		DriverType: "telegram",
		Enabled:    true,
	})
	mux := http.NewServeMux()
	Routes(mux, engine, "test")

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/deliveries/"+id+"/commands/enable", nil)
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body=%s", rec.Code, http.StatusOK, rec.Body.String())
	}
	profiles := engine.DeliveryProfiles()
	if len(profiles) != 1 || !profiles[0].InboundCommandsEnabled {
		t.Fatalf("profile commands enabled = %+v, want true", profiles)
	}
}
