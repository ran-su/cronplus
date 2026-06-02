package api

import (
	"fmt"
	"log"
	"net"
	"net/http"

	"github.com/ran-su/cronplus/internal/core"
)

type ServerInfo struct {
	Version           string `json:"version"`
	Addr              string `json:"addr,omitempty"`
	ConfigDir         string `json:"configDir,omitempty"`
	TokenPath         string `json:"tokenPath,omitempty"`
	StatePath         string `json:"statePath,omitempty"`
	MaxConcurrentRuns int    `json:"maxConcurrentRuns,omitempty"`
}

// Server is the HTTP server that serves the API and web UI.
type Server struct {
	engine *core.Engine
	token  string
	addr   string
	info   ServerInfo
}

// NewServer creates a new API server.
func NewServer(engine *core.Engine, token, addr, version string) *Server {
	return NewServerWithInfo(engine, token, addr, ServerInfo{Version: version, Addr: addr})
}

// NewServerWithInfo creates a new API server with daemon metadata exposed in status.
func NewServerWithInfo(engine *core.Engine, token, addr string, info ServerInfo) *Server {
	if info.Version == "" {
		info.Version = "dev"
	}
	if info.Addr == "" {
		info.Addr = addr
	}
	return &Server{
		engine: engine,
		token:  token,
		addr:   addr,
		info:   info,
	}
}

// Build creates the http.Server with all routes and middleware configured.
// Returns the server so the caller can manage its lifecycle (graceful shutdown).
func (s *Server) Build(webFS http.FileSystem) *http.Server {
	mux := http.NewServeMux()

	// Register API routes
	RoutesWithInfo(mux, s.engine, s.info)

	// Serve embedded web UI
	mux.Handle("/", StaticHandler(webFS))

	// Wrap with middleware: CORS → Auth
	allowedOrigins := allowedUIOrigins(s.addr)
	handler := CORSMiddleware(allowedOrigins, AuthMiddleware(s.token, allowedOrigins, mux))

	log.Printf("[CronPlus] Web server listening on http://%s", s.addr)
	fmt.Printf("\n  ╭──────────────────────────────────────────╮\n")
	fmt.Printf("  │  CronPlus %s%s│\n", s.info.Version, padding(s.info.Version, 33))
	fmt.Printf("  │  Web UI:  http://%s%s│\n", s.addr, padding(s.addr, 24))
	fmt.Printf("  │  Auth:    ~/.config/cronplus/auth-token  │\n")
	fmt.Printf("  ╰──────────────────────────────────────────╯\n\n")

	return &http.Server{
		Addr:    s.addr,
		Handler: handler,
	}
}

func allowedUIOrigins(addr string) []string {
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return []string{"http://" + addr}
	}

	origins := []string{"http://" + net.JoinHostPort(host, port)}
	if host == "127.0.0.1" || host == "localhost" {
		origins = append(origins,
			"http://localhost:"+port,
			"http://127.0.0.1:"+port,
		)
	}
	if host == "::1" || host == "[::1]" || host == "localhost" {
		origins = append(origins, "http://[::1]:"+port)
	}
	return dedupeStrings(origins)
}

func dedupeStrings(in []string) []string {
	seen := make(map[string]bool, len(in))
	out := make([]string, 0, len(in))
	for _, value := range in {
		if value == "" || seen[value] {
			continue
		}
		seen[value] = true
		out = append(out, value)
	}
	return out
}

func padding(s string, width int) string {
	pad := width - len(s)
	if pad <= 0 {
		return ""
	}
	result := ""
	for i := 0; i < pad; i++ {
		result += " "
	}
	return result
}
