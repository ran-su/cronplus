package api

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"
)

// LoadOrCreateToken reads the auth token from file, or generates one on first run.
func LoadOrCreateToken(path string) (string, error) {
	if path == "" {
		home, _ := os.UserHomeDir()
		path = filepath.Join(home, ".config", "cronplus", "auth-token")
	}

	data, err := os.ReadFile(path)
	if err == nil {
		token := strings.TrimSpace(string(data))
		if token != "" {
			return token, nil
		}
	}

	token, err := generateToken(32)
	if err != nil {
		return "", fmt.Errorf("failed to generate token: %w", err)
	}

	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0700); err != nil {
		return "", fmt.Errorf("failed to create config dir: %w", err)
	}
	if err := os.WriteFile(path, []byte(token+"\n"), 0600); err != nil {
		return "", fmt.Errorf("failed to write token file: %w", err)
	}

	log.Printf("[CronPlus] Auth token written to %s", path)
	return token, nil
}

func generateToken(bytes int) (string, error) {
	b := make([]byte, bytes)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

// AuthMiddleware returns middleware that checks Bearer token authentication.
// Static asset paths and the auth/check endpoint are excluded.
func AuthMiddleware(token string, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path := r.URL.Path

		// Static assets: no auth required
		if !strings.HasPrefix(path, "/api/") {
			next.ServeHTTP(w, r)
			return
		}

		// Auth check endpoint: no auth required (localhost-only)
		if path == "/api/auth/check" {
			handleAuthCheck(w, r, token)
			return
		}

		// Check Bearer token in header
		auth := r.Header.Get("Authorization")
		if strings.HasPrefix(auth, "Bearer ") {
			if strings.TrimSpace(auth[7:]) == token {
				next.ServeHTTP(w, r)
				return
			}
		}

		// Check token in query param (for SSE EventSource which can't set headers)
		if qToken := r.URL.Query().Get("token"); qToken == token {
			next.ServeHTTP(w, r)
			return
		}

		if auth == "" {
			http.Error(w, `{"error":"unauthorized","message":"Authentication required."}`, http.StatusUnauthorized)
			return
		}

		http.Error(w, `{"error":"unauthorized","message":"Invalid token."}`, http.StatusUnauthorized)
	})
}

// handleAuthCheck returns the token for localhost connections only.
func handleAuthCheck(w http.ResponseWriter, r *http.Request, token string) {
	remoteAddr := r.RemoteAddr
	// Extract IP from addr:port
	host := remoteAddr
	if idx := strings.LastIndex(remoteAddr, ":"); idx >= 0 {
		host = remoteAddr[:idx]
	}
	// Strip brackets from IPv6
	host = strings.Trim(host, "[]")

	if host == "127.0.0.1" || host == "::1" || host == "localhost" {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{"token":"%s"}`, token)
		return
	}

	http.Error(w, `{"error":"forbidden","message":"Auth check is only available from localhost."}`, http.StatusForbidden)
}
