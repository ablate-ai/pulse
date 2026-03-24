package serverapi

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"pulse/internal/nodes"
	"pulse/internal/users"
)

func TestNodeSettingsReturnsCertificate(t *testing.T) {
	api := &systemAPI{
		users:             users.NewMemoryStore(),
		nodes:             nodes.NewMemoryStore(),
		base:              New(nodes.NewMemoryStore(), nodes.ClientOptions{}),
		nodeClientCertPEM: "-----BEGIN CERTIFICATE-----\nTEST\n-----END CERTIFICATE-----\n",
	}

	req := httptest.NewRequest(http.MethodGet, "/v1/node/settings", nil)
	rec := httptest.NewRecorder()
	api.handleNodeSettings(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}

	var body struct {
		Certificate string `json:"certificate"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if body.Certificate == "" {
		t.Fatalf("expected certificate in response")
	}
}
