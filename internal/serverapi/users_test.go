package serverapi

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"pulse/internal/inbounds"
	"pulse/internal/jobs"
	"pulse/internal/nodes"
	"pulse/internal/users"
)

// setupTestMux 创建带 mock 节点的测试 mux，返回 mux 和 ibStore。
func setupTestMux(t *testing.T, nodeHandler func(path string, w http.ResponseWriter, r *http.Request)) (*http.ServeMux, *inbounds.MemoryStore) {
	t.Helper()

	nodeMux := http.NewServeMux()
	nodeMux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		nodeHandler(r.URL.Path, w, r)
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

	ibStore := inbounds.NewMemoryStore()
	userAPI := newUserAPI(users.NewMemoryStore(), nodeStore, ibStore, baseAPI, jobs.ApplyOptions{})
	mux := http.NewServeMux()
	userAPI.Register(mux)

	return mux, ibStore
}

// createUser 辅助：POST /v1/users，返回 user ID。
func createUser(t *testing.T, mux *http.ServeMux, body string) string {
	t.Helper()
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/users", bytes.NewReader([]byte(body)))
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("create user status = %d body=%s", rec.Code, rec.Body.String())
	}
	var out map[string]any
	_ = json.NewDecoder(rec.Body).Decode(&out)
	return out["id"].(string)
}

// createUserInbound 辅助：POST /v1/users/{userID}/inbounds，返回 inbound ID。
func createUserInbound(t *testing.T, mux *http.ServeMux, userID, nodeID string) string {
	t.Helper()
	body := `{"node_id":"` + nodeID + `"}`
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/users/"+userID+"/inbounds", bytes.NewReader([]byte(body)))
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("create user inbound status = %d body=%s", rec.Code, rec.Body.String())
	}
	var out map[string]any
	_ = json.NewDecoder(rec.Body).Decode(&out)
	return out["id"].(string)
}

func TestUserSubscriptionAndApplyFlow(t *testing.T) {
	var capturedConfig string
	mux, ibStore := setupTestMux(t, func(path string, w http.ResponseWriter, r *http.Request) {
		switch path {
		case "/v1/node/runtime/restart":
			var req map[string]string
			_ = json.NewDecoder(r.Body).Decode(&req)
			capturedConfig = req["config"]
			if !strings.Contains(req["config"], "\"type\": \"vless\"") {
				http.Error(w, "bad config", http.StatusBadRequest)
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"running": true})
		}
	})

	// 在 ibStore 中注册 vless 入站和对应 host
	_, _ = ibStore.UpsertInbound(inbounds.Inbound{
		ID:       "ib-vless",
		NodeID:   "node-1",
		Protocol: "vless",
		Tag:      "pulse-vless-node-1",
		Port:     443,
	})
	_, _ = ibStore.UpsertHost(inbounds.Host{
		ID:        "host-1",
		InboundID: "ib-vless",
		Address:   "example.com",
		Port:      443,
	})

	// 创建 alice 并绑定节点
	aliceID := createUser(t, mux, `{"id":"user-1","username":"alice"}`)
	ibID := createUserInbound(t, mux, aliceID, "node-1")

	// 获取订阅链接
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/v1/users/"+aliceID+"/inbounds/"+ibID+"/subscription", nil)
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("subscription status = %d body=%s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "vless://") {
		t.Fatalf("expected vless link, got %s", rec.Body.String())
	}

	// 下发配置
	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodPost, "/v1/users/"+aliceID+"/inbounds/"+ibID+"/apply", nil)
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("apply status = %d body=%s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "\"running\":true") {
		t.Fatalf("expected running node status, got %s", rec.Body.String())
	}

	// 创建 bob 并绑定同一节点
	bobID := createUser(t, mux, `{"id":"user-2","username":"bob"}`)
	ibID2 := createUserInbound(t, mux, bobID, "node-1")

	// 下发 bob 的配置
	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodPost, "/v1/users/"+bobID+"/inbounds/"+ibID2+"/apply", nil)
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("apply second user status = %d body=%s", rec.Code, rec.Body.String())
	}

	// 配置中应包含两个用户
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
	userAPI := newUserAPI(users.NewMemoryStore(), nodeStore, inbounds.NewMemoryStore(), baseAPI, jobs.ApplyOptions{})
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
	mux, ibStore := setupTestMux(t, func(path string, w http.ResponseWriter, r *http.Request) {
		if path == "/v1/node/runtime/restart" {
			_ = json.NewEncoder(w).Encode(map[string]any{"running": true})
		}
	})

	// 在 node-1 上注册 trojan 和 shadowsocks 入站
	_, _ = ibStore.UpsertInbound(inbounds.Inbound{
		ID:       "ib-trojan",
		NodeID:   "node-1",
		Protocol: "trojan",
		Tag:      "pulse-trojan-node-1",
		Port:     443,
	})
	_, _ = ibStore.UpsertHost(inbounds.Host{
		ID:        "host-trojan",
		InboundID: "ib-trojan",
		Address:   "example.com",
		Port:      443,
	})
	_, _ = ibStore.UpsertInbound(inbounds.Inbound{
		ID:       "ib-ss",
		NodeID:   "node-1",
		Protocol: "shadowsocks",
		Tag:      "pulse-ss-node-1",
		Port:     8443,
		Method:   "aes-256-gcm",
	})
	_, _ = ibStore.UpsertHost(inbounds.Host{
		ID:        "host-ss",
		InboundID: "ib-ss",
		Address:   "example.com",
		Port:      8443,
	})

	// 创建用户并绑定节点
	userID := createUser(t, mux, `{"id":"user-multi","username":"alice"}`)
	ibID := createUserInbound(t, mux, userID, "node-1")

	// 订阅应包含 trojan 和 ss 链接
	for _, want := range []string{"trojan://", "ss://"} {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/v1/users/"+userID+"/inbounds/"+ibID+"/subscription", nil)
		mux.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("subscription status = %d body=%s", rec.Code, rec.Body.String())
		}
		if !strings.Contains(rec.Body.String(), want) {
			t.Fatalf("expected %s link, got %s", want, rec.Body.String())
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

	ibStore := inbounds.NewMemoryStore()
	_, _ = ibStore.UpsertInbound(inbounds.Inbound{
		ID:       "ib-vless",
		NodeID:   "node-1",
		Protocol: "vless",
		Tag:      "pulse-vless-node-1",
		Port:     443,
	})

	userStore := users.NewMemoryStore()
	_, _ = userStore.UpsertUser(users.User{ID: "u1", Username: "alice", Status: users.StatusActive, TrafficLimit: 100})
	_, _ = userStore.UpsertUser(users.User{ID: "u2", Username: "bob", Status: users.StatusActive})
	_, _ = userStore.UpsertUserInbound(users.UserInbound{ID: "u1-ib0", UserID: "u1", NodeID: "node-1", UUID: "uuid-alice", Secret: "secret-alice"})
	_, _ = userStore.UpsertUserInbound(users.UserInbound{ID: "u2-ib0", UserID: "u2", NodeID: "node-1", UUID: "uuid-bob", Secret: "secret-bob"})

	result, err := jobs.SyncUsage(t.Context(), userStore, nodeStore, ibStore, baseAPI.Dial, jobs.ApplyOptions{})
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
