package sqlite

import (
	"database/sql"
	"path/filepath"
	"testing"
	"time"

	"pulse/internal/nodes"
	"pulse/internal/users"
)

func TestSQLiteStoresPersistData(t *testing.T) {
	path := filepath.Join(t.TempDir(), "pulse.db")

	db, err := Open(path)
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}

	nodeStore := db.NodeStore()
	userStore := db.UserStore()

	_, err = nodeStore.Upsert(nodes.Node{
		ID:      "node-1",
		Name:    "node 1",
		BaseURL: "https://127.0.0.1:8081",
	})
	if err != nil {
		t.Fatalf("node upsert error = %v", err)
	}

	_, err = userStore.Upsert(users.User{
		ID:            "user-1",
		Username:      "alice",
		UUID:          "bf000d23-0752-40b4-affe-68f7707a9661",
		Protocol:      "trojan",
		Secret:        "trojan-pass",
		Method:        "aes-128-gcm",
		NodeID:        "node-1",
		Domain:        "example.com",
		Port:          443,
		ApplyCount:    3,
		LastAppliedAt: time.Now().UTC().Truncate(time.Second),
	})
	if err != nil {
		t.Fatalf("user upsert error = %v", err)
	}

	if err := db.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}

	db, err = Open(path)
	if err != nil {
		t.Fatalf("reopen Open() error = %v", err)
	}
	defer db.Close()

	node, err := db.NodeStore().Get("node-1")
	if err != nil {
		t.Fatalf("Get(node) error = %v", err)
	}
	if node.Name != "node 1" {
		t.Fatalf("unexpected node name: %s", node.Name)
	}

	list, err := db.UserStore().ListByNode("node-1")
	if err != nil {
		t.Fatalf("ListByNode() error = %v", err)
	}
	if len(list) != 1 || list[0].Username != "alice" {
		t.Fatalf("unexpected users: %#v", list)
	}
	if list[0].Protocol != "trojan" || list[0].ApplyCount != 3 {
		t.Fatalf("unexpected user fields: %#v", list[0])
	}
}

func TestOpenMigratesLegacyUsersTable(t *testing.T) {
	path := filepath.Join(t.TempDir(), "legacy.db")

	conn, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatalf("sql.Open() error = %v", err)
	}
	_, err = conn.Exec(`
		CREATE TABLE users (
			id TEXT PRIMARY KEY,
			username TEXT NOT NULL,
			uuid TEXT NOT NULL,
			node_id TEXT NOT NULL,
			domain TEXT NOT NULL,
			port INTEGER NOT NULL,
			inbound_tag TEXT NOT NULL,
			created_at TEXT NOT NULL
		);
	`)
	if err != nil {
		t.Fatalf("create legacy users table error = %v", err)
	}
	if err := conn.Close(); err != nil {
		t.Fatalf("close legacy db error = %v", err)
	}

	db, err := Open(path)
	if err != nil {
		t.Fatalf("Open() with legacy schema error = %v", err)
	}
	defer db.Close()

	_, err = db.UserStore().Upsert(users.User{
		ID:       "user-1",
		Username: "alice",
		UUID:     "bf000d23-0752-40b4-affe-68f7707a9661",
		NodeID:   "node-1",
		Domain:   "example.com",
		Port:     443,
	})
	if err != nil {
		t.Fatalf("user upsert after migration error = %v", err)
	}
}
