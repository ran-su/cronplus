package daemonclient

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
)

// Client talks to the local CronPlus daemon HTTP API.
type Client struct {
	BaseURL    string
	Token      string
	HTTPClient *http.Client
}

// Error is a daemon API error with the JSON error code preserved.
type Error struct {
	StatusCode int
	Code       string
	Message    string
}

func (e *Error) Error() string {
	if e == nil {
		return ""
	}
	if e.Code == "" {
		return e.Message
	}
	return e.Code + ": " + e.Message
}

// New creates a local daemon client.
func New(baseURL, token string) *Client {
	return &Client{
		BaseURL: strings.TrimRight(baseURL, "/"),
		Token:   strings.TrimSpace(token),
	}
}

// DefaultBaseURL resolves the daemon URL using the same precedence as CLI API commands.
func DefaultBaseURL() (string, error) {
	if port := strings.TrimSpace(os.Getenv("CRONPLUS_PORT")); port != "" {
		return "http://127.0.0.1:" + port, nil
	}

	configDir, err := defaultConfigDir()
	if err != nil {
		return "", err
	}
	if port, err := readDaemonLockPort(filepath.Join(configDir, "daemon.lock")); err == nil && port > 0 {
		return fmt.Sprintf("http://127.0.0.1:%d", port), nil
	}
	return "http://127.0.0.1:9876", nil
}

// ReadDefaultToken reads the daemon bearer token from the default config path.
func ReadDefaultToken() (string, error) {
	configDir, err := defaultConfigDir()
	if err != nil {
		return "", err
	}
	data, err := os.ReadFile(filepath.Join(configDir, "auth-token"))
	if err != nil {
		return "", err
	}
	return string(bytes.TrimSpace(data)), nil
}

// Get sends an authenticated GET request and decodes the JSON response.
func (c *Client) Get(path string) (any, error) {
	return c.Request(http.MethodGet, path, nil)
}

// Post sends an authenticated POST request and decodes the JSON response.
func (c *Client) Post(path string, body any) (any, error) {
	return c.Request(http.MethodPost, path, body)
}

// Put sends an authenticated PUT request and decodes the JSON response.
func (c *Client) Put(path string, body any) (any, error) {
	return c.Request(http.MethodPut, path, body)
}

// Delete sends an authenticated DELETE request and decodes the JSON response.
func (c *Client) Delete(path string) (any, error) {
	return c.Request(http.MethodDelete, path, nil)
}

// Request sends an authenticated daemon API request and decodes the JSON response.
func (c *Client) Request(method, path string, body any) (any, error) {
	if strings.TrimSpace(c.Token) == "" {
		return nil, &Error{Code: "auth_token_missing", Message: "CronPlus auth token was not found. Start the daemon once so ~/.config/cronplus/auth-token exists."}
	}
	if strings.TrimSpace(c.BaseURL) == "" {
		return nil, &Error{Code: "daemon_url_missing", Message: "CronPlus daemon URL could not be resolved."}
	}

	var reader io.Reader
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			return nil, err
		}
		reader = bytes.NewReader(data)
	}

	req, err := http.NewRequest(method, c.BaseURL+path, reader)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+c.Token)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	httpClient := c.HTTPClient
	if httpClient == nil {
		httpClient = http.DefaultClient
	}
	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, &Error{Code: "daemon_unavailable", Message: err.Error()}
	}
	defer resp.Body.Close()

	data, err := io.ReadAll(io.LimitReader(resp.Body, 10<<20))
	if err != nil {
		return nil, err
	}
	if resp.StatusCode >= 400 {
		var apiErr struct {
			Error   string `json:"error"`
			Message string `json:"message"`
		}
		if err := json.Unmarshal(data, &apiErr); err == nil && (apiErr.Error != "" || apiErr.Message != "") {
			return nil, &Error{StatusCode: resp.StatusCode, Code: apiErr.Error, Message: apiErr.Message}
		}
		return nil, &Error{StatusCode: resp.StatusCode, Code: "http_error", Message: strings.TrimSpace(string(data))}
	}
	if len(bytes.TrimSpace(data)) == 0 {
		return map[string]any{}, nil
	}
	var decoded any
	if err := json.Unmarshal(data, &decoded); err != nil {
		return nil, err
	}
	return decoded, nil
}
