package serverapi

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"pulse/internal/jobs"
	"pulse/internal/nodes"
	"pulse/internal/users"
)

// createTestUserAndInbound 辅助函数：先创建 User，再创建 UserInbound，返回 inbound ID。
func createTestUserAndInbound(t *testing.T, mux *http.ServeMux, userBody, inboundBody string, userID string) string {
	t.Helper()

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/users", bytes.NewReader([]byte(userBody)))
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("create user status = %d body=%s", rec.Code, rec.Body.String())
	}

	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodPost, "/v1/users/"+userID+"/inbounds", bytes.NewReader([]byte(inboundBody)))
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("create inbound status = %d body=%s", rec.Code, rec.Body.String())
	}

	var out map[string]any
	if err := json.NewDecoder(rec.Body).Decode(&out); err != nil {
		t.Fatalf("decode inbound: %v", err)
	}
	return out["id"].(string)
}

func TestUserSubscriptionAndApplyFlow(t *testing.T) {
	var capturedConfig string
	nodeMux := http.NewServeMux()
	nodeMux.HandleFunc("/v1/node/runtime/restart", func(w http.ResponseWriter, r *http.Request) {
		var req map[string]string
		_ = json.NewDecoder(r.Body).Decode(&req)
		capturedConfig = req["config"]
		if !strings.Contains(req["config"], "\"type\": \"vless\"") {
			http.Error(w, "bad config", http.StatusBadRequest)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"running": true})
	})

	nodeStore := nodes.NewMemoryStore()
	_, _ = nodeStore.Upsert(nodes.Node{
		ID:      "node-1",
		Name:    "node 1",
		BaseURL: "http://node.test",
	})

	baseAPI := New(nodeStore, nodes.ClientOptions{})
	baseAPI.clientFactory = func(node nodes.Node) *nodes.Client {
		return nodes.NewClientWithHTTPClient(node.BaseURL, &http.Client{
			Transport: roundTripperFunc(func(req *http.Request) (*http.Response, error) {
				rec := httptest.NewRecorder()
				nodeMux.ServeHTTP(rec, req)
				return rec.Result(), nil
			}),
		})
	}

	userAPI := newUserAPI(users.NewMemoryStore(), nodeStore, baseAPI, jobs.ApplyOptions{})
	mux := http.NewServeMux()
	userAPI.Register(mux)

	// 创建用户和入站
	ibID := createTestUserAndInbound(t, mux,
		`{"id":"user-1","username":"alice"}`,
		`{"id":"ib-1","node_id":"node-1","domain":"example.com","port":443}`,
		"user-1",
	)

	// 获取订阅链接
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/v1/users/user-1/inbounds/"+ibID+"/subscription", nil)
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("subscription status = %d body=%s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "vless://") {
		t.Fatalf("expected vless link, got %s", rec.Body.String())
	}

	// 下发配置
	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodPost, "/v1/users/user-1/inbounds/"+ibID+"/apply", nil)
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("apply status = %d body=%s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "\"running\":true") {
		t.Fatalf("expected running node status, got %s", rec.Body.String())
	}

	// 创建第二个用户和入站
	ibID2 := createTestUserAndInbound(t, mux,
		`{"id":"user-2","username":"bob"}`,
		`{"id":"ib-2","node_id":"node-1","domain":"example.com","port":443}`,
		"user-2",
	)

	// 下发第二个用户
	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodPost, "/v1/users/user-2/inbounds/"+ibID2+"/apply", nil)
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("apply second user status = %d body=%s", rec.Code, rec.Body.String())
	}

	if !strings.Contains(capturedConfig, "\"name\": \"alice\"") || !strings.Contains(capturedConfig, "\"name\": \"bob\"") {
		t.Fatalf("expected aggregated config with both users, got %s", capturedConfig)
	}
}

func TestCreateUserAutoGeneratesID(t *testing.T) {
	nodeStore := nodes.NewMemoryStore()
	_, _ = nodeStore.Upsert(nodes.Node{
		ID:      "node-1",
		Name:    "node 1",
		BaseURL: "http://node.test",
	})

	baseAPI := New(nodeStore, nodes.ClientOptions{})
	userAPI := newUserAPI(users.NewMemoryStore(), nodeStore, baseAPI, jobs.ApplyOptions{})
	mux := http.NewServeMux()
	userAPI.Register(mux)

	body := []byte(`{"username":"alice"}`)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/users", bytes.NewReader(body))
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("create user status = %d body=%s", rec.Code, rec.Body.String())
	}

	var out users.User
	if err := json.NewDecoder(rec.Body).Decode(&out); err != nil {
		t.Fatalf("decode user: %v", err)
	}
	if out.ID == "" {
		t.Fatal("expected generated user id")
	}
}

func TestUserSupportsMultipleProtocols(t *testing.T) {
	nodeStore := nodes.NewMemoryStore()
	_, _ = nodeStore.Upsert(nodes.Node{
		ID:      "node-1",
		Name:    "node 1",
		BaseURL: "http://node.test",
	})

	baseAPI := New(nodeStore, nodes.ClientOptions{})
	baseAPI.clientFactory = func(node nodes.Node) *nodes.Client {
		return nodes.NewClientWithHTTPClient(node.BaseURL, &http.Client{
			Transport: roundTripperFunc(func(req *http.Request) (*http.Response, error) {
				rec := httptest.NewRecorder()
				if req.URL.Path == "/v1/node/runtime/restart" {
					_ = json.NewEncoder(rec).Encode(map[string]any{"running": true})
				} else {
					http.NotFound(rec, req)
				}
				return rec.Result(), nil
			}),
		})
	}

	userAPI := newUserAPI(users.NewMemoryStore(), nodeStore, baseAPI, jobs.ApplyOptions{})
	mux := http.NewServeMux()
	userAPI.Register(mux)

	// 先创建用户，再创建各协议入站
	ibIDTrojan := createTestUserAndInbound(t, mux,
		`{"id":"user-trojan","username":"alice"}`,
		`{"id":"ib-trojan","protocol":"trojan","secret":"trojan-pass","node_id":"node-1","domain":"example.com","port":443}`,
		"user-trojan",
	)
	ibIDSS := createTestUserAndInbound(t, mux,
		`{"id":"user-ss","username":"bob"}`,
		`{"id":"ib-ss","protocol":"shadowsocks","secret":"ss-pass","method":"aes-256-gcm","node_id":"node-1","domain":"example.com","port":8443}`,
		"user-ss",
	)

	for _, tc := range []struct {
		userID string
		ibID   string
		want   string
	}{
		{userID: "user-trojan", ibID: ibIDTrojan, want: "trojan://"},
		{userID: "user-ss", ibID: ibIDSS, want: "ss://"},
	} {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/v1/users/"+tc.userID+"/inbounds/"+tc.ibID+"/subscription", nil)
		mux.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("subscription status = %d body=%s", rec.Code, rec.Body.String())
		}
		if !strings.Contains(rec.Body.String(), tc.want) {
			t.Fatalf("expected %s link, got %s", tc.want, rec.Body.String())
		}
	}
}

func TestSyncUsageDisablesLimitedUserAndReloadsNode(t *testing.T) {
	var capturedConfig string
	nodeStore := nodes.NewMemoryStore()
	_, _ = nodeStore.Upsert(nodes.Node{
		ID:      "node-1",
		Name:    "node 1",
		BaseURL: "http://node.test",
	})

	baseAPI := New(nodeStore, nodes.ClientOptions{})
	baseAPI.clientFactory = func(node nodes.Node) *nodes.Client {
		return nodes.NewClientWithHTTPClient(node.BaseURL, &http.Client{
			Transport: roundTripperFunc(func(req *http.Request) (*http.Response, error) {
				rec := httptest.NewRecorder()
				switch req.URL.Path {
				case "/v1/node/runtime/usage":
					_ = json.NewEncoder(rec).Encode(map[string]any{
						"available":      true,
						"running":        true,
						"upload_total":   100,
						"download_total": 200,
						"connections":    1,
						"users": []map[string]any{
							{"user": "alice", "upload_total": 80, "download_total": 40, "connections": 1},
							{"user": "bob", "upload_total": 10, "download_total": 10, "connections": 0},
						},
					})
				case "/v1/node/runtime/restart":
					var reqBody map[string]string
					_ = json.NewDecoder(req.Body).Decode(&reqBody)
					capturedConfig = reqBody["config"]
					_ = json.NewEncoder(rec).Encode(map[string]any{"running": true})
				default:
					http.NotFound(rec, req)
				}
				return rec.Result(), nil
			}),
		})
	}

	userStore := users.NewMemoryStore()
	_, _ = userStore.UpsertUser(users.User{ID: "u1", Username: "alice", Status: users.StatusActive, TrafficLimit: 100})
	_, _ = userStore.UpsertUser(users.User{ID: "u2", Username: "bob", Status: users.StatusActive})
	_, _ = userStore.UpsertUserInbound(users.UserInbound{ID: "u1-ib0", UserID: "u1", NodeID: "node-1", Protocol: "vless", Domain: "example.com", Port: 443})
	_, _ = userStore.UpsertUserInbound(users.UserInbound{ID: "u2-ib0", UserID: "u2", NodeID: "node-1", Protocol: "vless", Domain: "example.com", Port: 443})

	result, err := jobs.SyncUsage(t.Context(), userStore, nodeStore, baseAPI.Dial, jobs.ApplyOptions{})
	if err != nil {
		t.Fatalf("SyncUsage() error = %v", err)
	}
	if result.NodesReloaded != 1 {
		t.Fatalf("expected 1 node reload, got %#v", result)
	}

	alice, err := userStore.GetUser("u1")
	if err != nil {
		t.Fatalf("GetUser(alice) error = %v", err)
	}
	if alice.EffectiveEnabled() {
		t.Fatalf("expected alice disabled after exceeding limit: %#v", alice)
	}
	if alice.UsedBytes != 120 {
		t.Fatalf("expected alice used bytes 120, got %d", alice.UsedBytes)
	}

	bob, err := userStore.GetUser("u2")
	if err != nil {
		t.Fatalf("GetUser(bob) error = %v", err)
	}
	if !bob.EffectiveEnabled() {
		t.Fatalf("expected bob to remain enabled")
	}
	if !strings.Contains(capturedConfig, "\"name\": \"bob\"") || strings.Contains(capturedConfig, "\"name\": \"alice\"") {
		t.Fatalf("expected config to keep only bob, got %s", capturedConfig)
	}
}
