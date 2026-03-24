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

	lastApplied := time.Now().UTC().Truncate(time.Second)
	_, _ = userStore.Upsert(users.User{ID: "u1", Username: "alice", Enabled: true, NodeID: "node-1", Domain: "example.com", Port: 443, Protocol: "vless", ApplyCount: 2, UploadBytes: 10, DownloadBytes: 20})
	_, _ = userStore.Upsert(users.User{ID: "u2", Username: "bob", Enabled: true, NodeID: "node-1", Domain: "example.com", Port: 8443, Protocol: "trojan", ApplyCount: 1, LastAppliedAt: lastApplied, TrafficLimit: 100, UploadBytes: 30, DownloadBytes: 40})
	_, _ = userStore.Upsert(users.User{ID: "u3", Username: "carol", Enabled: false, NodeID: "node-2", Domain: "example.com", Port: 9443, Protocol: "shadowsocks", TrafficLimit: 50, UploadBytes: 30, DownloadBytes: 30})

	summary, err := Build(nodeStore, userStore)
	if err != nil {
		t.Fatalf("Build() error = %v", err)
	}

	if summary.NodesCount != 2 || summary.UsersCount != 3 {
		t.Fatalf("unexpected counts: %#v", summary)
	}
	if summary.Protocols["vless"] != 1 || summary.Protocols["trojan"] != 1 || summary.Protocols["shadowsocks"] != 1 {
		t.Fatalf("unexpected protocols: %#v", summary.Protocols)
	}
	if summary.TotalApplyCount != 3 {
		t.Fatalf("unexpected total apply count: %d", summary.TotalApplyCount)
	}
	if summary.TotalUploadBytes != 70 || summary.TotalDownloadBytes != 90 || summary.TotalUsedBytes != 160 {
		t.Fatalf("unexpected byte totals: %#v", summary)
	}
	if summary.LimitedUsersCount != 2 || summary.DisabledUsersCount != 1 {
		t.Fatalf("unexpected limited/disabled counts: %#v", summary)
	}
	if !summary.LastAppliedAt.Equal(lastApplied) {
		t.Fatalf("unexpected last applied at: %v", summary.LastAppliedAt)
	}
}
