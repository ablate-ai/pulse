package usage

import (
	"testing"

	"pulse/internal/nodes"
	"pulse/internal/users"
)

func TestBuild(t *testing.T) {
	nodeStore := nodes.NewMemoryStore()
	userStore := users.NewMemoryStore()

	_, _ = nodeStore.Upsert(nodes.Node{ID: "node-1", Name: "node-1", BaseURL: "http://127.0.0.1:8081"})
	_, _ = nodeStore.Upsert(nodes.Node{ID: "node-2", Name: "node-2", BaseURL: "http://127.0.0.1:8082"})

	_, _ = userStore.UpsertUser(users.User{ID: "u1", Username: "alice", Status: users.StatusActive, UploadBytes: 10, DownloadBytes: 20})
	_, _ = userStore.UpsertUser(users.User{ID: "u2", Username: "bob", Status: users.StatusActive, TrafficLimit: 100, UploadBytes: 30, DownloadBytes: 40})
	_, _ = userStore.UpsertUser(users.User{ID: "u3", Username: "carol", Status: users.StatusDisabled, TrafficLimit: 50, UploadBytes: 30, DownloadBytes: 30})

	_, _ = userStore.UpsertUserInbound(users.UserInbound{ID: "u1-ib0", UserID: "u1", NodeID: "node-1", UUID: "uuid-alice", Secret: "secret-alice"})
	_, _ = userStore.UpsertUserInbound(users.UserInbound{ID: "u2-ib0", UserID: "u2", NodeID: "node-1", UUID: "uuid-bob", Secret: "secret-bob"})
	_, _ = userStore.UpsertUserInbound(users.UserInbound{ID: "u3-ib0", UserID: "u3", NodeID: "node-2", UUID: "uuid-carol", Secret: "secret-carol"})

	summary, err := Build(nodeStore, userStore)
	if err != nil {
		t.Fatalf("Build() error = %v", err)
	}

	if summary.NodesCount != 2 || summary.UsersCount != 3 {
		t.Fatalf("unexpected counts: %#v", summary)
	}
	if summary.TotalUploadBytes != 70 || summary.TotalDownloadBytes != 90 || summary.TotalUsedBytes != 160 {
		t.Fatalf("unexpected byte totals: %#v", summary)
	}
	if summary.LimitedUsersCount != 2 || summary.DisabledUsersCount != 1 {
		t.Fatalf("unexpected limited/disabled counts: %#v", summary)
	}
}
