package jobs

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"pulse/internal/nodes"
	"pulse/internal/users"
)

// ─── ShouldResetTraffic ───────────────────────────────────────────────────────

func TestShouldResetTraffic_NoReset(t *testing.T) {
	created := time.Now().Add(-365 * 24 * time.Hour)
	if ShouldResetTraffic(users.ResetStrategyNoReset, created, nil, time.Now()) {
		t.Fatal("no_reset 策略不应触发重置")
	}
}

func TestShouldResetTraffic_Day(t *testing.T) {
	created := time.Now().Add(-25 * time.Hour)
	if !ShouldResetTraffic(users.ResetStrategyDay, created, nil, time.Now()) {
		t.Fatal("超过 24h 应触发 day 重置")
	}
	created = time.Now().Add(-23 * time.Hour)
	if ShouldResetTraffic(users.ResetStrategyDay, created, nil, time.Now()) {
		t.Fatal("未超过 24h 不应触发 day 重置")
	}
}

func TestShouldResetTraffic_Month(t *testing.T) {
	created := time.Now().AddDate(0, -1, -1)
	if !ShouldResetTraffic(users.ResetStrategyMonth, created, nil, time.Now()) {
		t.Fatal("超过一个月应触发 month 重置")
	}
	created = time.Now().AddDate(0, 0, -15)
	if ShouldResetTraffic(users.ResetStrategyMonth, created, nil, time.Now()) {
		t.Fatal("未超过一个月不应触发 month 重置")
	}
}

func TestShouldResetTraffic_UseLastResetAt(t *testing.T) {
	created := time.Now().Add(-100 * 24 * time.Hour)
	lastReset := time.Now().Add(-23 * time.Hour)
	// 上次重置在 23h 前，day 策略还没到
	if ShouldResetTraffic(users.ResetStrategyDay, created, &lastReset, time.Now()) {
		t.Fatal("上次重置在 23h 前，day 策略不应再次触发")
	}
	lastReset = time.Now().Add(-25 * time.Hour)
	if !ShouldResetTraffic(users.ResetStrategyDay, created, &lastReset, time.Now()) {
		t.Fatal("上次重置在 25h 前，day 策略应触发")
	}
}

// ─── ResetTraffic ─────────────────────────────────────────────────────────────

func TestResetTraffic_ResetsLimitedUser(t *testing.T) {
	nodeStore := nodes.NewMemoryStore()
	userStore := users.NewMemoryStore()
	_, _ = nodeStore.Upsert(nodes.Node{ID: "n1", Name: "n1", BaseURL: "http://node.test"})

	// 用户创建于 25h 前，使用了 500 bytes，限额 400
	created := time.Now().Add(-25 * time.Hour)
	u := users.User{
		ID: "u1", Username: "alice", Status: users.StatusActive,
		DataLimitResetStrategy: users.ResetStrategyDay,
		TrafficLimit:           400,
		UploadBytes:            300, DownloadBytes: 200,
		CreatedAt: created,
	}
	u.UsedBytes = u.UploadBytes + u.DownloadBytes
	_, _ = userStore.UpsertUser(u)

	// 创建对应的 UserInbound
	_, _ = userStore.UpsertUserInbound(users.UserInbound{
		ID:       "u1-ib0",
		UserID:   "u1",
		NodeID:   "n1",
		Protocol: "vless",
		Domain:   "example.com",
		Port:     443,
	})

	var restarted bool
	dial := testDial(t, func(path string, w http.ResponseWriter, r *http.Request) {
		if path == "/v1/node/runtime/restart" {
			restarted = true
			_ = json.NewEncoder(w).Encode(map[string]any{"running": true})
		}
	})

	result, err := ResetTraffic(context.Background(), userStore, nodeStore, dial, ApplyOptions{})
	if err != nil {
		t.Fatalf("ResetTraffic() error = %v", err)
	}
	if result.UsersReset != 1 {
		t.Fatalf("expected 1 user reset, got %d", result.UsersReset)
	}
	if !restarted {
		t.Fatal("expected node restart after reset")
	}

	alice, _ := userStore.GetUser("u1")
	if alice.UsedBytes != 0 || alice.UploadBytes != 0 || alice.DownloadBytes != 0 {
		t.Fatalf("expected bytes cleared, got used=%d", alice.UsedBytes)
	}
	if alice.LastTrafficResetAt == nil {
		t.Fatal("expected LastTrafficResetAt set")
	}
	// 重置后 EffectiveStatus 应该回到 active
	if !alice.EffectiveEnabled() {
		t.Fatal("expected alice active after reset")
	}
}

// ─── SyncUsage ────────────────────────────────────────────────────────────────

func TestSyncUsage_UpdatesBytesAndReloads(t *testing.T) {
	nodeStore := nodes.NewMemoryStore()
	userStore := users.NewMemoryStore()
	_, _ = nodeStore.Upsert(nodes.Node{ID: "n1", Name: "n1", BaseURL: "http://node.test"})
	_, _ = userStore.UpsertUser(users.User{
		ID: "u1", Username: "alice", Status: users.StatusActive,
		TrafficLimit: 100,
	})
	_, _ = userStore.UpsertUserInbound(users.UserInbound{
		ID:       "u1-ib0",
		UserID:   "u1",
		NodeID:   "n1",
		Protocol: "vless",
		Domain:   "example.com",
		Port:     443,
	})

	dial := testDial(t, func(path string, w http.ResponseWriter, r *http.Request) {
		switch path {
		case "/v1/node/runtime/usage":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"available": true, "running": true,
				"users": []map[string]any{
					{"user": "alice", "upload_total": 80, "download_total": 30},
				},
			})
		case "/v1/node/runtime/stop":
			_ = json.NewEncoder(w).Encode(map[string]any{"running": false})
		}
	})

	result, err := SyncUsage(context.Background(), userStore, nodeStore, dial, ApplyOptions{})
	if err != nil {
		t.Fatalf("SyncUsage() error = %v", err)
	}
	if result.UsersUpdated != 1 || result.NodesStopped != 1 {
		t.Fatalf("unexpected result: %+v", result)
	}

	alice, _ := userStore.GetUser("u1")
	if alice.UsedBytes != 110 {
		t.Fatalf("expected used=110, got %d", alice.UsedBytes)
	}
	if alice.EffectiveEnabled() {
		t.Fatal("expected alice limited after exceeding quota")
	}
}

// ─── 辅助 ─────────────────────────────────────────────────────────────────────

func testDial(t *testing.T, handler func(path string, w http.ResponseWriter, r *http.Request)) NodeDialer {
	t.Helper()
	nodeMux := http.NewServeMux()
	nodeMux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		handler(r.URL.Path, w, r)
	})
	return func(nodeID string) (*nodes.Client, error) {
		return nodes.NewClientWithHTTPClient("http://node.test", &http.Client{
			Transport: roundTripperFunc(func(req *http.Request) (*http.Response, error) {
				rec := httptest.NewRecorder()
				nodeMux.ServeHTTP(rec, req)
				return rec.Result(), nil
			}),
		}), nil
	}
}

type roundTripperFunc func(*http.Request) (*http.Response, error)

func (f roundTripperFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
