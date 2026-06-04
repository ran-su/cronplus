package api

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"log"
	"net"
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
	} else if !os.IsNotExist(err) {
		return "", fmt.Errorf("failed to read token file: %w", err)
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
func AuthMiddleware(token string, allowedOrigins []string, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path := r.URL.Path

		// Static assets: no auth required
		if !strings.HasPrefix(path, "/api/") {
			next.ServeHTTP(w, r)
			return
		}

		// Auth check endpoint: no auth required (localhost-only)
		if path == "/api/auth/check" {
			handleAuthCheck(w, r, token, allowedOrigins)
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

		// Check token in query param only for SSE EventSource, which can't set headers.
		if path == "/api/events" && r.URL.Query().Get("token") == token {
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
func handleAuthCheck(w http.ResponseWriter, r *http.Request, token string, allowedOrigins []string) {
	if !isLoopbackRemote(r.RemoteAddr) {
		http.Error(w, `{"error":"forbidden","message":"Auth check is only available from localhost."}`, http.StatusForbidden)
		return
	}

	// Cross-origin pages can also connect to 127.0.0.1, so localhost alone is
	// not a sufficient browser security boundary. Same-origin UI requests do
	// not send Origin; CORS requests do.
	if origin := r.Header.Get("Origin"); origin != "" && !originAllowed(origin, allowedOrigins) {
		http.Error(w, `{"error":"forbidden","message":"Auth check is only available to the CronPlus UI origin."}`, http.StatusForbidden)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	fmt.Fprintf(w, `{"token":"%s"}`, token)
}

func isLoopbackRemote(remoteAddr string) bool {
	host, _, err := net.SplitHostPort(remoteAddr)
	if err != nil {
		host = remoteAddr
	}
	host = strings.Trim(host, "[]")
	if host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

func originAllowed(origin string, allowedOrigins []string) bool {
	origin = strings.TrimSpace(origin)
	if origin == "" {
		return false
	}
	for _, allowed := range allowedOrigins {
		if origin == allowed {
			return true
		}
	}
	return false
}
