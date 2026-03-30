package usage

import (
	"testing"
	"time"

	"pulse/internal/nodes"
	"pulse/internal/users"
)

func TestBuild(t *testing.T) {
	nodeStore := nodes.NewMemoryStore()
	userStore := users.NewMemoryStore()

	_, _ = nodeStore.Upsert(nodes.Node{ID: "node-1", Name: "node-1", BaseURL: "http://127.0.0.1:8081"})
	_, _ = nodeStore.Upsert(nodes.Node{ID: "node-2", Name: "node-2", BaseURL: "http://127.0.0.1:8082"})
	// 模拟节点累计流量（alice+bob 走 node-1，carol 走 node-2）
	_ = nodeStore.AddTraffic("node-1", 40, 70)
	_ = nodeStore.AddTraffic("node-2", 30, 30)

	recentOnline := time.Now().Add(-1 * time.Minute)   // 1 分钟前，在线
	staleOnline := time.Now().Add(-10 * time.Minute)   // 10 分钟前，离线

	_, _ = userStore.UpsertUser(users.User{ID: "u1", Username: "alice", Status: users.StatusActive, UploadBytes: 10, DownloadBytes: 20, OnlineAt: &recentOnline})
	// bob 超限（UsedBytes=80 >= TrafficLimit=70），在线
	_, _ = userStore.UpsertUser(users.User{ID: "u2", Username: "bob", Status: users.StatusActive, TrafficLimit: 70, UploadBytes: 30, DownloadBytes: 50, OnlineAt: &recentOnline})
	// carol 禁用，10 分钟前有流量，视为离线
	_, _ = userStore.UpsertUser(users.User{ID: "u3", Username: "carol", Status: users.StatusDisabled, TrafficLimit: 50, UploadBytes: 30, DownloadBytes: 30, OnlineAt: &staleOnline})

	_, _ = userStore.UpsertUserInbound(users.UserInbound{ID: "u1-ib0", UserID: "u1", NodeID: "node-1", UUID: "uuid-alice", Secret: "secret-alice"})
	_, _ = userStore.UpsertUserInbound(users.UserInbound{ID: "u2-ib0", UserID: "u2", NodeID: "node-1", UUID: "uuid-bob", Secret: "secret-bob"})
	_, _ = userStore.UpsertUserInbound(users.UserInbound{ID: "u3-ib0", UserID: "u3", NodeID: "node-2", UUID: "uuid-carol", Secret: "secret-carol"})

	summary, err := Build(nodeStore, userStore, 14)
	if err != nil {
		t.Fatalf("Build() error = %v", err)
	}

	if summary.NodesCount != 2 || summary.UsersCount != 3 {
		t.Fatalf("unexpected counts: %#v", summary)
	}
	if summary.TotalUploadBytes != 70 || summary.TotalDownloadBytes != 100 || summary.TotalUsedBytes != 170 {
		t.Fatalf("unexpected byte totals: %#v", summary)
	}
	if summary.ActiveUsersCount != 1 || summary.LimitedUsersCount != 1 || summary.DisabledUsersCount != 1 || summary.ExpiredUsersCount != 0 {
		t.Fatalf("unexpected status counts: %#v", summary)
	}
	if summary.OnlineUsersCount != 2 {
		t.Fatalf("expected 2 online users, got %d", summary.OnlineUsersCount)
	}
}
