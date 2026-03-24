package nodes

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"
)

type Client struct {
	baseURL    string
	httpClient *http.Client
	initErr    error
}

type ClientOptions struct {
	ClientCertFile string
	ClientKeyFile  string
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

func NewClient(node Node, options ClientOptions) *Client {
	httpClient, err := buildHTTPClient(node, options)
	return &Client{
		baseURL:    strings.TrimRight(node.BaseURL, "/"),
		httpClient: httpClient,
		initErr:    err,
	}
}

func NewClientWithHTTPClient(baseURL string, httpClient *http.Client) *Client {
	return &Client{
		baseURL:    strings.TrimRight(baseURL, "/"),
		httpClient: httpClient,
	}
}

func buildHTTPClient(node Node, options ClientOptions) (*http.Client, error) {
	httpClient := &http.Client{
		Timeout: 5 * time.Second,
	}
	if !strings.HasPrefix(strings.TrimSpace(node.BaseURL), "https://") {
		return httpClient, nil
	}

	transport := http.DefaultTransport.(*http.Transport).Clone()
	tlsConfig := &tls.Config{
		MinVersion: tls.VersionTLS12,
	}
	var clientPair *tls.Certificate
	if options.ClientCertFile != "" || options.ClientKeyFile != "" {
		if options.ClientCertFile == "" || options.ClientKeyFile == "" {
			return nil, fmt.Errorf("client cert file and key file must be configured together")
		}
		pair, err := tls.LoadX509KeyPair(options.ClientCertFile, options.ClientKeyFile)
		if err != nil {
			return nil, fmt.Errorf("load client key pair: %w", err)
		}
		clientPair = &pair
		tlsConfig.Certificates = []tls.Certificate{pair}
	}
	serverCert, err := fetchServerCertificatePEM(node.BaseURL, clientPair)
	if err != nil {
		return nil, err
	}
	roots := x509.NewCertPool()
	if !roots.AppendCertsFromPEM([]byte(serverCert)) {
		return nil, fmt.Errorf("parse node certificate")
	}
	tlsConfig.InsecureSkipVerify = true
	tlsConfig.VerifyConnection = func(cs tls.ConnectionState) error {
		if len(cs.PeerCertificates) == 0 {
			return fmt.Errorf("node tls handshake did not present a certificate")
		}
		opts := x509.VerifyOptions{
			Roots:         roots,
			Intermediates: x509.NewCertPool(),
		}
		for _, cert := range cs.PeerCertificates[1:] {
			opts.Intermediates.AddCert(cert)
		}
		_, err := cs.PeerCertificates[0].Verify(opts)
		if err != nil {
			return fmt.Errorf("verify node certificate: %w", err)
		}
		return nil
	}
	transport.TLSClientConfig = tlsConfig
	httpClient.Transport = transport
	return httpClient, nil
}

func fetchServerCertificatePEM(rawURL string, clientPair *tls.Certificate) (string, error) {
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return "", fmt.Errorf("parse node url: %w", err)
	}
	address := parsed.Host
	if _, _, err := net.SplitHostPort(address); err != nil {
		address = net.JoinHostPort(parsed.Hostname(), "443")
	}
	tlsConfig := &tls.Config{
		InsecureSkipVerify: true,
		MinVersion:         tls.VersionTLS12,
	}
	if clientPair != nil {
		tlsConfig.Certificates = []tls.Certificate{*clientPair}
	}

	conn, err := tls.Dial("tcp", address, tlsConfig)
	if err != nil {
		return "", fmt.Errorf("dial node tls: %w", err)
	}
	defer conn.Close()

	state := conn.ConnectionState()
	if len(state.PeerCertificates) == 0 {
		return "", fmt.Errorf("node did not present a certificate")
	}
	pemBytes := pemEncodeCertificate(state.PeerCertificates[0].Raw)
	return string(pemBytes), nil
}

func pemEncodeCertificate(raw []byte) []byte {
	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: raw})
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

type CertPaths struct {
	Domain   string `json:"domain"`
	CertPath string `json:"cert_path"`
	KeyPath  string `json:"key_path"`
}

func (c *Client) EnsureCert(ctx context.Context, domain string) (CertPaths, error) {
	var out CertPaths
	err := c.do(ctx, http.MethodPost, "/v1/node/cert/ensure", map[string]string{"domain": domain}, &out)
	return out, err
}

func (c *Client) do(ctx context.Context, method, path string, body any, out any) error {
	if c.initErr != nil {
		return fmt.Errorf("configure node client: %w", c.initErr)
	}
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
