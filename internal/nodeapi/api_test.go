package nodeapi

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"pulse/internal/singbox"
)

func TestRuntimeEndpointRequiresAuthAndReturnsInfo(t *testing.T) {
	manager := singbox.NewManager()
	api := New(manager, "secret-token")

	mux := http.NewServeMux()
	api.Register(mux)

	req := httptest.NewRequest(http.MethodGet, "/v1/node/runtime", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", rec.Code)
	}

	req = httptest.NewRequest(http.MethodGet, "/v1/node/runtime", nil)
	req.Header.Set("Authorization", "Bearer secret-token")
	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}

	var body struct {
		Available bool   `json:"available"`
		Module    string `json:"module"`
		Version   string `json:"version"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatalf("decode body: %v", err)
	}

	if !body.Available {
		t.Fatalf("expected available runtime")
	}
	if body.Module == "" {
		t.Fatalf("expected module")
	}
	if body.Version == "" {
		t.Fatalf("expected version")
	}
}

func TestUsageEndpointRequiresAuthAndReturnsStats(t *testing.T) {
	manager := singbox.NewManager()
	api := New(manager, "secret-token")

	mux := http.NewServeMux()
	api.Register(mux)

	req := httptest.NewRequest(http.MethodGet, "/v1/node/runtime/usage", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", rec.Code)
	}

	req = httptest.NewRequest(http.MethodGet, "/v1/node/runtime/usage", nil)
	req.Header.Set("Authorization", "Bearer secret-token")
	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}

	var body struct {
		Available   bool `json:"available"`
		Connections int  `json:"connections"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if body.Available {
		t.Fatalf("expected unavailable usage before start")
	}
	if body.Connections != 0 {
		t.Fatalf("expected zero connections, got %d", body.Connections)
	}
}
