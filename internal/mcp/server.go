package mcp

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"

	"github.com/ran-su/cronplus/internal/daemonclient"
)

const protocolVersion = "2025-06-18"

type Server struct {
	client  *daemonclient.Client
	version string
}

type rpcRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method,omitempty"`
	Params  json.RawMessage `json:"params,omitempty"`
}

type rpcResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Result  any             `json:"result,omitempty"`
	Error   *rpcError       `json:"error,omitempty"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

func NewServer(client *daemonclient.Client, version string) *Server {
	if version == "" {
		version = "dev"
	}
	return &Server{client: client, version: version}
}

// Run serves newline-delimited JSON-RPC MCP messages over stdio-compatible streams.
func (s *Server) Run(ctx context.Context, in io.Reader, out io.Writer) error {
	scanner := bufio.NewScanner(in)
	scanner.Buffer(make([]byte, 0, 64*1024), 10<<20)
	for scanner.Scan() {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}
		response, ok := s.HandleMessage(scanner.Bytes())
		if !ok {
			continue
		}
		if _, err := out.Write(response); err != nil {
			return err
		}
		if _, err := out.Write([]byte("\n")); err != nil {
			return err
		}
	}
	return scanner.Err()
}

// HandleMessage handles one JSON-RPC message. It returns ok=false for notifications.
func (s *Server) HandleMessage(data []byte) ([]byte, bool) {
	data = []byte(strings.TrimSpace(string(data)))
	if len(data) == 0 {
		return nil, false
	}

	var req rpcRequest
	if err := json.Unmarshal(data, &req); err != nil {
		return encodeResponse(rawNull(), nil, &rpcError{Code: -32700, Message: "Parse error"}), true
	}
	if len(req.ID) == 0 {
		return nil, false
	}
	if req.JSONRPC != "" && req.JSONRPC != "2.0" {
		return encodeResponse(req.ID, nil, &rpcError{Code: -32600, Message: "Invalid JSON-RPC version"}), true
	}
	if strings.TrimSpace(req.Method) == "" {
		return encodeResponse(req.ID, nil, &rpcError{Code: -32600, Message: "Invalid request: missing method"}), true
	}

	result, rpcErr := s.handleRequest(req.Method, req.Params)
	return encodeResponse(req.ID, result, rpcErr), true
}

func (s *Server) handleRequest(method string, params json.RawMessage) (any, *rpcError) {
	switch method {
	case "initialize":
		return map[string]any{
			"protocolVersion": protocolVersion,
			"capabilities":    s.capabilities(),
			"serverInfo":      s.serverInfo(),
			"instructions":    "Use CronPlus MCP tools to inspect, validate, import, run, and debug local CronPlus task packages through the running daemon.",
		}, nil
	case "server/discover":
		return map[string]any{
			"protocolVersions": []string{protocolVersion, "2025-03-26", "2024-11-05"},
			"capabilities":     s.capabilities(),
			"serverInfo":       s.serverInfo(),
		}, nil
	case "ping":
		return map[string]any{}, nil
	case "tools/list":
		return s.listTools(), nil
	case "tools/call":
		return s.callTool(params)
	case "resources/list":
		return s.listResources(), nil
	case "resources/templates/list":
		return s.listResourceTemplates(), nil
	case "resources/read":
		return s.readResource(params)
	case "prompts/list":
		return s.listPrompts(), nil
	case "prompts/get":
		return s.getPrompt(params)
	default:
		return nil, &rpcError{Code: -32601, Message: "Method not found: " + method}
	}
}

func (s *Server) capabilities() map[string]any {
	return map[string]any{
		"tools": map[string]any{
			"listChanged": false,
		},
		"resources": map[string]any{
			"listChanged": false,
			"subscribe":   false,
		},
		"prompts": map[string]any{
			"listChanged": false,
		},
	}
}

func (s *Server) serverInfo() map[string]string {
	return map[string]string{
		"name":    "cronplus",
		"version": s.version,
	}
}

func encodeResponse(id json.RawMessage, result any, rpcErr *rpcError) []byte {
	if len(id) == 0 {
		id = rawNull()
	}
	resp := rpcResponse{JSONRPC: "2.0", ID: id, Result: result, Error: rpcErr}
	data, err := json.Marshal(resp)
	if err != nil {
		fallback := rpcResponse{
			JSONRPC: "2.0",
			ID:      id,
			Error:   &rpcError{Code: -32603, Message: "Internal error: " + err.Error()},
		}
		data, _ = json.Marshal(fallback)
	}
	return data
}

func rawNull() json.RawMessage {
	return json.RawMessage("null")
}

func decodeParams(params json.RawMessage, v any) *rpcError {
	if len(params) == 0 {
		params = json.RawMessage("{}")
	}
	if err := json.Unmarshal(params, v); err != nil {
		return &rpcError{Code: -32602, Message: "Invalid params: " + err.Error()}
	}
	return nil
}

func invalidParams(message string) *rpcError {
	return &rpcError{Code: -32602, Message: "Invalid params: " + message}
}

func unknownTool(name string) *rpcError {
	return &rpcError{Code: -32602, Message: "Unknown tool: " + name}
}

func textBlock(text string) map[string]string {
	return map[string]string{"type": "text", "text": text}
}

func prettyJSON(value any) string {
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return fmt.Sprint(value)
	}
	return string(data)
}
