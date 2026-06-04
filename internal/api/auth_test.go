package api

import (
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
)

func TestAuthCheckRejectsCrossOriginLocalhost(t *testing.T) {
	allowedOrigins := []string{"http://127.0.0.1:9876"}
	handler := CORSMiddleware(allowedOrigins, AuthMiddleware("secret", allowedOrigins, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("next handler should not be called")
	})))

	req := httptest.NewRequest(http.MethodGet, "http://127.0.0.1:9876/api/auth/check", nil)
	req.RemoteAddr = "127.0.0.1:54321"
	req.Header.Set("Origin", "https://example.com")
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want %d; body=%s", rec.Code, http.StatusForbidden, rec.Body.String())
	}
	if got := rec.Header().Get("Access-Control-Allow-Origin"); got != "" {
		t.Fatalf("Access-Control-Allow-Origin = %q, want empty", got)
	}
	if strings.Contains(rec.Body.String(), "secret") {
		t.Fatal("auth check leaked the token in a forbidden response")
	}
}

func TestAuthCheckAllowsLocalhostWithoutOrigin(t *testing.T) {
	allowedOrigins := []string{"http://127.0.0.1:9876"}
	handler := AuthMiddleware("secret", allowedOrigins, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("next handler should not be called")
	}))

	req := httptest.NewRequest(http.MethodGet, "http://127.0.0.1:9876/api/auth/check", nil)
	req.RemoteAddr = "127.0.0.1:54321"
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body=%s", rec.Code, http.StatusOK, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"token":"secret"`) {
		t.Fatalf("response body = %q, want token", rec.Body.String())
	}
}

func TestQueryTokenOnlyAuthenticatesEvents(t *testing.T) {
	allowedOrigins := []string{"http://127.0.0.1:9876"}
	handler := AuthMiddleware("secret", allowedOrigins, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))

	statusReq := httptest.NewRequest(http.MethodGet, "http://127.0.0.1:9876/api/status?token=secret", nil)
	statusReq.RemoteAddr = "127.0.0.1:54321"
	statusRec := httptest.NewRecorder()
	handler.ServeHTTP(statusRec, statusReq)
	if statusRec.Code != http.StatusUnauthorized {
		t.Fatalf("/api/status status = %d, want %d", statusRec.Code, http.StatusUnauthorized)
	}

	eventsReq := httptest.NewRequest(http.MethodGet, "http://127.0.0.1:9876/api/events?token=secret", nil)
	eventsReq.RemoteAddr = "127.0.0.1:54321"
	eventsRec := httptest.NewRecorder()
	handler.ServeHTTP(eventsRec, eventsReq)
	if eventsRec.Code != http.StatusNoContent {
		t.Fatalf("/api/events status = %d, want %d", eventsRec.Code, http.StatusNoContent)
	}
}

func TestLoadOrCreateTokenReturnsReadErrors(t *testing.T) {
	_, err := LoadOrCreateToken(t.TempDir())
	if err == nil {
		t.Fatal("LoadOrCreateToken error is nil, want read error")
	}
	if !strings.Contains(err.Error(), "failed to read token file") {
		t.Fatalf("error = %q, want read diagnostic", err.Error())
	}
}

func TestLoadOrCreateTokenCreatesMissingFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "auth-token")
	token, err := LoadOrCreateToken(path)
	if err != nil {
		t.Fatalf("LoadOrCreateToken: %v", err)
	}
	if len(token) != 64 {
		t.Fatalf("token length = %d, want 64 hex chars", len(token))
	}
	again, err := LoadOrCreateToken(path)
	if err != nil {
		t.Fatalf("LoadOrCreateToken second call: %v", err)
	}
	if again != token {
		t.Fatalf("second token = %q, want stable token %q", again, token)
	}
}

func TestWriteJSONDoesNotOverrideCORSMiddleware(t *testing.T) {
	allowedOrigins := []string{"http://127.0.0.1:9876"}
	handler := CORSMiddleware(allowedOrigins, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
	}))

	evilReq := httptest.NewRequest(http.MethodGet, "http://127.0.0.1:9876/api/status", nil)
	evilReq.Header.Set("Origin", "https://example.com")
	evilRec := httptest.NewRecorder()
	handler.ServeHTTP(evilRec, evilReq)
	if got := evilRec.Header().Get("Access-Control-Allow-Origin"); got != "" {
		t.Fatalf("evil Access-Control-Allow-Origin = %q, want empty", got)
	}

	allowedReq := httptest.NewRequest(http.MethodGet, "http://127.0.0.1:9876/api/status", nil)
	allowedReq.Header.Set("Origin", "http://127.0.0.1:9876")
	allowedRec := httptest.NewRecorder()
	handler.ServeHTTP(allowedRec, allowedReq)
	if got := allowedRec.Header().Get("Access-Control-Allow-Origin"); got != "http://127.0.0.1:9876" {
		t.Fatalf("allowed Access-Control-Allow-Origin = %q, want allowed origin", got)
	}
}
