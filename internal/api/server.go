package api

import (
	"fmt"
	"log"
	"net/http"

	"github.com/ran-su/cronplus/internal/core"
)

// Server is the HTTP server that serves the API and web UI.
type Server struct {
	engine  *core.Engine
	token   string
	addr    string
	version string
}

// NewServer creates a new API server.
func NewServer(engine *core.Engine, token, addr, version string) *Server {
	return &Server{
		engine:  engine,
		token:   token,
		addr:    addr,
		version: version,
	}
}

// Build creates the http.Server with all routes and middleware configured.
// Returns the server so the caller can manage its lifecycle (graceful shutdown).
func (s *Server) Build(webFS http.FileSystem) *http.Server {
	mux := http.NewServeMux()

	// Register API routes
	Routes(mux, s.engine, s.version)

	// Serve embedded web UI
	mux.Handle("/", StaticHandler(webFS))

	// Wrap with middleware: CORS → Auth
	handler := CORSMiddleware(AuthMiddleware(s.token, mux))

	log.Printf("[CronPlus] Web server listening on http://%s", s.addr)
	fmt.Printf("\n  ╭──────────────────────────────────────────╮\n")
	fmt.Printf("  │  CronPlus %s%s│\n", s.version, padding(s.version, 33))
	fmt.Printf("  │  Web UI:  http://%s%s│\n", s.addr, padding(s.addr, 24))
	fmt.Printf("  │  Auth:    ~/.config/cronplus/auth-token  │\n")
	fmt.Printf("  ╰──────────────────────────────────────────╯\n\n")

	return &http.Server{
		Addr:    s.addr,
		Handler: handler,
	}
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
