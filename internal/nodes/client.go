package nodes

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"
)

type Client struct {
	baseURL    string
	authToken  string
	httpClient *http.Client
}

type RuntimeInfo struct {
	Available bool   `json:"available"`
	Module    string `json:"module"`
	Version   string `json:"version,omitempty"`
	LastError string `json:"last_error,omitempty"`
}

type Status struct {
	Running   bool      `json:"running"`
	StartedAt time.Time `json:"started_at,omitempty"`
}

type LogsResponse struct {
	Logs []string `json:"logs"`
}

type UsageStats struct {
	Available     bool        `json:"available"`
	Running       bool        `json:"running"`
	StartedAt     time.Time   `json:"started_at,omitempty"`
	UploadTotal   int64       `json:"upload_total"`
	DownloadTotal int64       `json:"download_total"`
	Connections   int         `json:"connections"`
	Users         []UserUsage `json:"users"`
}

type UserUsage struct {
	User          string `json:"user"`
	UploadTotal   int64  `json:"upload_total"`
	DownloadTotal int64  `json:"download_total"`
	Connections   int    `json:"connections"`
}

type ConfigRequest struct {
	Config string `json:"config"`
}

func NewClient(baseURL, authToken string) *Client {
	return NewClientWithHTTPClient(baseURL, authToken, &http.Client{
		Timeout: 5 * time.Second,
	})
}

func NewClientWithHTTPClient(baseURL, authToken string, httpClient *http.Client) *Client {
	return &Client{
		baseURL:    strings.TrimRight(baseURL, "/"),
		authToken:  authToken,
		httpClient: httpClient,
	}
}

func (c *Client) Runtime(ctx context.Context) (RuntimeInfo, error) {
	var out RuntimeInfo
	err := c.do(ctx, http.MethodGet, "/v1/node/runtime", nil, &out)
	return out, err
}

func (c *Client) Status(ctx context.Context) (Status, error) {
	var out Status
	err := c.do(ctx, http.MethodGet, "/v1/node/runtime/status", nil, &out)
	return out, err
}

func (c *Client) Logs(ctx context.Context) (LogsResponse, error) {
	var out LogsResponse
	err := c.do(ctx, http.MethodGet, "/v1/node/runtime/logs", nil, &out)
	return out, err
}

func (c *Client) Usage(ctx context.Context) (UsageStats, error) {
	var out UsageStats
	err := c.do(ctx, http.MethodGet, "/v1/node/runtime/usage", nil, &out)
	return out, err
}

func (c *Client) Start(ctx context.Context, req ConfigRequest) (Status, error) {
	var out Status
	err := c.do(ctx, http.MethodPost, "/v1/node/runtime/start", req, &out)
	return out, err
}

func (c *Client) Stop(ctx context.Context) (Status, error) {
	var out Status
	err := c.do(ctx, http.MethodPost, "/v1/node/runtime/stop", nil, &out)
	return out, err
}

func (c *Client) Restart(ctx context.Context, req ConfigRequest) (Status, error) {
	var out Status
	err := c.do(ctx, http.MethodPost, "/v1/node/runtime/restart", req, &out)
	return out, err
}

func (c *Client) do(ctx context.Context, method, path string, body any, out any) error {
	var bodyReader *bytes.Reader
	if body == nil {
		bodyReader = bytes.NewReader(nil)
	} else {
		payload, err := json.Marshal(body)
		if err != nil {
			return fmt.Errorf("marshal request: %w", err)
		}
		bodyReader = bytes.NewReader(payload)
	}

	req, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, bodyReader)
	if err != nil {
		return fmt.Errorf("build request: %w", err)
	}
	if c.authToken != "" {
		req.Header.Set("Authorization", "Bearer "+c.authToken)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("request node: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		var apiErr struct {
			Error string `json:"error"`
		}
		_ = json.NewDecoder(resp.Body).Decode(&apiErr)
		if apiErr.Error == "" {
			apiErr.Error = resp.Status
		}
		return fmt.Errorf("node api error: %s", apiErr.Error)
	}

	if out == nil {
		return nil
	}

	if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
		return fmt.Errorf("decode response: %w", err)
	}
	return nil
}
