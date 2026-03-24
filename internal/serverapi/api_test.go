package serverapi

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"pulse/internal/nodes"
)

func TestNodeLifecycleAndProxyEndpoints(t *testing.T) {
	nodeMux := http.NewServeMux()
	nodeMux.HandleFunc("/v1/node/runtime", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"available": true,
			"version":   "v1.13.3",
		})
	})
	nodeMux.HandleFunc("/v1/node/runtime/status", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"running": true,
		})
	})
	nodeMux.HandleFunc("/v1/node/runtime/usage", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"available":      true,
			"running":        true,
			"upload_total":   128,
			"download_total": 256,
			"connections":    2,
		})
	})
	nodeMux.HandleFunc("/v1/node/runtime/start", func(w http.ResponseWriter, r *http.Request) {
		var req map[string]string
		_ = json.NewDecoder(r.Body).Decode(&req)
		if req["config"] == "" {
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"running": true,
		})
	})

	store := nodes.NewMemoryStore()
	api := New(store, nodes.ClientOptions{})
	api.clientFactory = func(node nodes.Node) *nodes.Client {
		return nodes.NewClientWithHTTPClient(node.BaseURL, &http.Client{
			Transport: roundTripperFunc(func(req *http.Request) (*http.Response, error) {
				rec := httptest.NewRecorder()
				nodeMux.ServeHTTP(rec, req)
				return rec.Result(), nil
			}),
		})
	}
	mux := http.NewServeMux()
	api.Register(mux)

	upsertBody := []byte(`{"id":"node-1","name":"node 1","base_url":"http://node.test"}`)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/nodes", bytes.NewReader(upsertBody))
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("upsert node status = %d", rec.Code)
	}

	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodGet, "/v1/nodes", nil)
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("list nodes status = %d", rec.Code)
	}

	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodGet, "/v1/nodes/node-1/runtime", nil)
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("runtime status = %d body=%s", rec.Code, rec.Body.String())
	}

	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodGet, "/v1/nodes/node-1/runtime/usage", nil)
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("runtime usage status = %d body=%s", rec.Code, rec.Body.String())
	}

	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodPost, "/v1/nodes/node-1/runtime/start", bytes.NewReader([]byte(`{"config":"{}"}`)))
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("start status = %d body=%s", rec.Code, rec.Body.String())
	}

	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodDelete, "/v1/nodes/node-1", nil)
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("delete status = %d", rec.Code)
	}
}

func TestCreateNodeAutoGeneratesID(t *testing.T) {
	store := nodes.NewMemoryStore()
	api := New(store, nodes.ClientOptions{})
	mux := http.NewServeMux()
	api.Register(mux)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/nodes", bytes.NewReader([]byte(`{"name":"node 1","base_url":"http://node.test"}`)))
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("create node status = %d body=%s", rec.Code, rec.Body.String())
	}

	var out nodes.Node
	if err := json.NewDecoder(rec.Body).Decode(&out); err != nil {
		t.Fatalf("decode node: %v", err)
	}
	if out.ID == "" {
		t.Fatal("expected generated node id")
	}
}

type roundTripperFunc func(req *http.Request) (*http.Response, error)

func (f roundTripperFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	req = req.Clone(req.Context())
	if req.Body == nil {
		req.Body = io.NopCloser(bytes.NewReader(nil))
	}
	return f(req)
}
