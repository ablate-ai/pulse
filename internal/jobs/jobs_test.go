package jobs

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"pulse/internal/inbounds"
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
	// 用固定时间避免月末 AddDate 规范化边界问题（如 3/30 - 1month = Feb30 → March2）
	now := time.Date(2026, 3, 15, 12, 0, 0, 0, time.UTC)
	created := now.AddDate(0, -1, -1) // 2026-02-14，超过一个月
	if !ShouldResetTraffic(users.ResetStrategyMonth, created, nil, now) {
		t.Fatal("超过一个月应触发 month 重置")
	}
	created = now.AddDate(0, 0, -15) // 2026-02-28，未超过一个月
	if ShouldResetTraffic(users.ResetStrategyMonth, created, nil, now) {
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
		ID:     "u1-ib0",
		UserID: "u1",
		NodeID: "n1",
		UUID:   "11111111-1111-1111-1111-111111111111",
		Secret: "test-secret",
	})

	ibStore := inbounds.NewMemoryStore()
	_, _ = ibStore.UpsertInbound(inbounds.Inbound{
		ID:       "ib-vless",
		NodeID:   "n1",
		Protocol: "vless",
		Tag:      "pulse-vless-n1",
		Port:     443,
	})

	var restarted bool
	dial := testDial(t, func(path string, w http.ResponseWriter, r *http.Request) {
		if path == "/v1/node/runtime/restart" {
			restarted = true
			_ = json.NewEncoder(w).Encode(map[string]any{"running": true})
		}
	})

	result, err := ResetTraffic(context.Background(), userStore, nodeStore, ibStore, dial, ApplyOptions{}, nil)
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
		ID:     "u1-ib0",
		UserID: "u1",
		NodeID: "n1",
		UUID:   "11111111-1111-1111-1111-111111111111",
		Secret: "test-secret",
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
		case "/v1/node/runtime/restart":
			_ = json.NewEncoder(w).Encode(map[string]any{"running": true})
		}
	})

	result, err := SyncUsage(context.Background(), userStore, nodeStore, inbounds.NewMemoryStore(), dial, ApplyOptions{}, nil)
	if err != nil {
		t.Fatalf("SyncUsage() error = %v", err)
	}
	if result.UsersUpdated != 1 || result.NodesReloaded != 1 {
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

// ─── SyncUsage TrafficRate ────────────────────────────────────────────────────

func TestSyncUsage_TrafficRate_Double(t *testing.T) {
	nodeStore := nodes.NewMemoryStore()
	userStore := users.NewMemoryStore()
	// 倍率 2.0：用户计费流量应翻倍
	_, _ = nodeStore.Upsert(nodes.Node{ID: "n1", Name: "n1", BaseURL: "http://node.test", TrafficRate: 2.0})
	_, _ = userStore.UpsertUser(users.User{
		ID: "u1", Username: "alice", Status: users.StatusActive,
		TrafficLimit: 999999,
	})
	_, _ = userStore.UpsertUserInbound(users.UserInbound{
		ID: "u1-ib0", UserID: "u1", NodeID: "n1",
		UUID: "11111111-1111-1111-1111-111111111111", Secret: "test-secret",
	})

	dial := usageDial(t, "alice", 80, 30)

	_, err := SyncUsage(context.Background(), userStore, nodeStore, inbounds.NewMemoryStore(), dial, ApplyOptions{}, nil)
	if err != nil {
		t.Fatalf("SyncUsage() error = %v", err)
	}

	alice, _ := userStore.GetUser("u1")
	// 用户计费流量 = (80+30) * 2 = 220
	if alice.UploadBytes != 160 {
		t.Errorf("upload: want 160, got %d", alice.UploadBytes)
	}
	if alice.DownloadBytes != 60 {
		t.Errorf("download: want 60, got %d", alice.DownloadBytes)
	}
	if alice.UsedBytes != 220 {
		t.Errorf("used: want 220, got %d", alice.UsedBytes)
	}

	// 节点记录真实流量，不受倍率影响
	node, _ := nodeStore.Get("n1")
	if node.UploadBytes != 80 {
		t.Errorf("node upload: want 80, got %d", node.UploadBytes)
	}
	if node.DownloadBytes != 30 {
		t.Errorf("node download: want 30, got %d", node.DownloadBytes)
	}
}

func TestSyncUsage_TrafficRate_Half(t *testing.T) {
	nodeStore := nodes.NewMemoryStore()
	userStore := users.NewMemoryStore()
	// 倍率 0.5：用户计费流量应减半
	_, _ = nodeStore.Upsert(nodes.Node{ID: "n1", Name: "n1", BaseURL: "http://node.test", TrafficRate: 0.5})
	_, _ = userStore.UpsertUser(users.User{
		ID: "u1", Username: "alice", Status: users.StatusActive,
		TrafficLimit: 999999,
	})
	_, _ = userStore.UpsertUserInbound(users.UserInbound{
		ID: "u1-ib0", UserID: "u1", NodeID: "n1",
		UUID: "11111111-1111-1111-1111-111111111111", Secret: "test-secret",
	})

	dial := usageDial(t, "alice", 80, 30)

	_, err := SyncUsage(context.Background(), userStore, nodeStore, inbounds.NewMemoryStore(), dial, ApplyOptions{}, nil)
	if err != nil {
		t.Fatalf("SyncUsage() error = %v", err)
	}

	alice, _ := userStore.GetUser("u1")
	// 用户计费流量 = (80+30) * 0.5 = 55
	if alice.UploadBytes != 40 {
		t.Errorf("upload: want 40, got %d", alice.UploadBytes)
	}
	if alice.DownloadBytes != 15 {
		t.Errorf("download: want 15, got %d", alice.DownloadBytes)
	}
	if alice.UsedBytes != 55 {
		t.Errorf("used: want 55, got %d", alice.UsedBytes)
	}

	// 节点记录真实流量，不受倍率影响
	node, _ := nodeStore.Get("n1")
	if node.UploadBytes != 80 {
		t.Errorf("node upload: want 80, got %d", node.UploadBytes)
	}
	if node.DownloadBytes != 30 {
		t.Errorf("node download: want 30, got %d", node.DownloadBytes)
	}
}

func TestSyncUsage_TrafficRate_QuotaWithRate(t *testing.T) {
	nodeStore := nodes.NewMemoryStore()
	userStore := users.NewMemoryStore()
	// 倍率 2.0，限额 100：实际产生 60 bytes 但计费 120，应触发 limited
	_, _ = nodeStore.Upsert(nodes.Node{ID: "n1", Name: "n1", BaseURL: "http://node.test", TrafficRate: 2.0})
	_, _ = userStore.UpsertUser(users.User{
		ID: "u1", Username: "alice", Status: users.StatusActive,
		TrafficLimit: 100,
	})
	_, _ = userStore.UpsertUserInbound(users.UserInbound{
		ID: "u1-ib0", UserID: "u1", NodeID: "n1",
		UUID: "11111111-1111-1111-1111-111111111111", Secret: "test-secret",
	})

	dial := usageDial(t, "alice", 40, 20) // 真实 60 bytes，计费 120

	_, err := SyncUsage(context.Background(), userStore, nodeStore, inbounds.NewMemoryStore(), dial, ApplyOptions{}, nil)
	if err != nil {
		t.Fatalf("SyncUsage() error = %v", err)
	}

	alice, _ := userStore.GetUser("u1")
	if alice.UsedBytes != 120 {
		t.Errorf("used: want 120, got %d", alice.UsedBytes)
	}
	if alice.EffectiveEnabled() {
		t.Error("alice should be limited (120 > 100) but is still enabled")
	}
}

// ─── SyncUsage MultiInbound ───────────────────────────────────────────────────

func TestSyncUsage_MultiInbound_NoDuplicateCounting(t *testing.T) {
	// 回归测试：一个用户在同一节点有多条 inbound 时，流量只应计算一次。
	// 修复前，流量会被乘以 inbound 数量（例如 4 条 inbound → 4 倍流量）。
	nodeStore := nodes.NewMemoryStore()
	userStore := users.NewMemoryStore()
	_, _ = nodeStore.Upsert(nodes.Node{ID: "n1", Name: "n1", BaseURL: "http://node.test"})
	_, _ = userStore.UpsertUser(users.User{
		ID: "u1", Username: "alice", Status: users.StatusActive,
		TrafficLimit: 999999,
	})
	// 同一个用户在同一节点上有 4 条 inbound（模拟 VLESS、VMess、Trojan、Shadowsocks）
	_, _ = userStore.UpsertUserInbound(users.UserInbound{
		ID: "u1-ib0", UserID: "u1", InboundID: "ib-vless", NodeID: "n1",
		UUID: "11111111-1111-1111-1111-111111111111", Secret: "s1",
	})
	_, _ = userStore.UpsertUserInbound(users.UserInbound{
		ID: "u1-ib1", UserID: "u1", InboundID: "ib-vmess", NodeID: "n1",
		UUID: "22222222-2222-2222-2222-222222222222", Secret: "s2",
	})
	_, _ = userStore.UpsertUserInbound(users.UserInbound{
		ID: "u1-ib2", UserID: "u1", InboundID: "ib-trojan", NodeID: "n1",
		UUID: "33333333-3333-3333-3333-333333333333", Secret: "s3",
	})
	_, _ = userStore.UpsertUserInbound(users.UserInbound{
		ID: "u1-ib3", UserID: "u1", InboundID: "ib-ss", NodeID: "n1",
		UUID: "44444444-4444-4444-4444-444444444444", Secret: "s4",
	})

	// 节点报告 alice 总共 upload=80, download=30（聚合值，不分 inbound）
	dial := usageDial(t, "alice", 80, 30)

	_, err := SyncUsage(context.Background(), userStore, nodeStore, inbounds.NewMemoryStore(), dial, ApplyOptions{}, nil)
	if err != nil {
		t.Fatalf("SyncUsage() error = %v", err)
	}

	alice, _ := userStore.GetUser("u1")
	// 流量应只计一次：80 + 30 = 110，而非 4 倍的 440
	if alice.UploadBytes != 80 {
		t.Errorf("upload: want 80, got %d", alice.UploadBytes)
	}
	if alice.DownloadBytes != 30 {
		t.Errorf("download: want 30, got %d", alice.DownloadBytes)
	}
	if alice.UsedBytes != 110 {
		t.Errorf("used: want 110, got %d", alice.UsedBytes)
	}

	// 节点流量也只应计一次
	node, _ := nodeStore.Get("n1")
	if node.UploadBytes != 80 {
		t.Errorf("node upload: want 80, got %d", node.UploadBytes)
	}
	if node.DownloadBytes != 30 {
		t.Errorf("node download: want 30, got %d", node.DownloadBytes)
	}

	// 所有 inbound 的游标都应更新到当前值
	for _, ibID := range []string{"u1-ib0", "u1-ib1", "u1-ib2", "u1-ib3"} {
		acc, _ := userStore.GetUserInbound(ibID)
		if acc.SyncedUploadBytes != 80 {
			t.Errorf("%s synced upload: want 80, got %d", ibID, acc.SyncedUploadBytes)
		}
		if acc.SyncedDownloadBytes != 30 {
			t.Errorf("%s synced download: want 30, got %d", ibID, acc.SyncedDownloadBytes)
		}
	}
}

func TestSyncUsage_MultiInbound_NewInboundAdded(t *testing.T) {
	// 测试：已有 inbound 游标在 50/20，新增一条 inbound（游标为 0）。
	// 节点报告 80/30 → delta 应基于最大游标 50/20 计算为 30/10，而非基于 0 计算为 80/30。
	nodeStore := nodes.NewMemoryStore()
	userStore := users.NewMemoryStore()
	_, _ = nodeStore.Upsert(nodes.Node{ID: "n1", Name: "n1", BaseURL: "http://node.test"})
	_, _ = userStore.UpsertUser(users.User{
		ID: "u1", Username: "alice", Status: users.StatusActive,
		TrafficLimit: 999999,
	})
	// 旧 inbound，已同步到 50/20
	_, _ = userStore.UpsertUserInbound(users.UserInbound{
		ID: "u1-ib0", UserID: "u1", InboundID: "ib-vless", NodeID: "n1",
		UUID: "11111111-1111-1111-1111-111111111111", Secret: "s1",
		SyncedUploadBytes: 50, SyncedDownloadBytes: 20,
	})
	// 新增 inbound，游标为 0
	_, _ = userStore.UpsertUserInbound(users.UserInbound{
		ID: "u1-ib1", UserID: "u1", InboundID: "ib-trojan", NodeID: "n1",
		UUID: "22222222-2222-2222-2222-222222222222", Secret: "s2",
		SyncedUploadBytes: 0, SyncedDownloadBytes: 0,
	})

	dial := usageDial(t, "alice", 80, 30)

	_, err := SyncUsage(context.Background(), userStore, nodeStore, inbounds.NewMemoryStore(), dial, ApplyOptions{}, nil)
	if err != nil {
		t.Fatalf("SyncUsage() error = %v", err)
	}

	alice, _ := userStore.GetUser("u1")
	// delta 应基于最大游标 (50, 20): upload delta=30, download delta=10
	if alice.UploadBytes != 30 {
		t.Errorf("upload: want 30, got %d", alice.UploadBytes)
	}
	if alice.DownloadBytes != 10 {
		t.Errorf("download: want 10, got %d", alice.DownloadBytes)
	}
	if alice.UsedBytes != 40 {
		t.Errorf("used: want 40, got %d", alice.UsedBytes)
	}
}

// ─── 辅助 ─────────────────────────────────────────────────────────────────────

// usageDial 返回一个固定上报 upload/download 的 dial 函数。
func usageDial(t *testing.T, username string, upload, download int64) NodeDialer {
	t.Helper()
	return testDial(t, func(path string, w http.ResponseWriter, r *http.Request) {
		switch path {
		case "/v1/node/runtime/usage":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"available": true, "running": true,
				"users": []map[string]any{
					{"user": username, "upload_total": upload, "download_total": download},
				},
			})
		case "/v1/node/runtime/restart":
			_ = json.NewEncoder(w).Encode(map[string]any{"running": true})
		}
	})
}



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
