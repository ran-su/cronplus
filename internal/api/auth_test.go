package api

import (
	"net/http"
	"net/http/httptest"
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
