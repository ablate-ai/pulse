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

	_, err = userStore.UpsertUser(users.User{
		ID:       "user-1",
		Username: "alice",
		Status:   users.StatusActive,
	})
	if err != nil {
		t.Fatalf("user upsert error = %v", err)
	}

	_, err = userStore.UpsertUserInbound(users.UserInbound{
		ID:            "user-1-ib0",
		UserID:        "user-1",
		NodeID:        "node-1",
		Protocol:      "trojan",
		UUID:          "bf000d23-0752-40b4-affe-68f7707a9661",
		Secret:        "trojan-pass",
		Method:        "aes-128-gcm",
		Domain:        "example.com",
		Port:          443,
		ApplyCount:    3,
		LastAppliedAt: time.Now().UTC().Truncate(time.Second),
	})
	if err != nil {
		t.Fatalf("user inbound upsert error = %v", err)
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

	list, err := db.UserStore().ListUserInboundsByNode("node-1")
	if err != nil {
		t.Fatalf("ListUserInboundsByNode() error = %v", err)
	}
	if len(list) != 1 || list[0].UserID != "user-1" {
		t.Fatalf("unexpected inbounds: %#v", list)
	}
	if list[0].Protocol != "trojan" || list[0].ApplyCount != 3 {
		t.Fatalf("unexpected inbound fields: %#v", list[0])
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
	// 插入一行旧数据
	_, err = conn.Exec(`
		INSERT INTO users (id, username, uuid, node_id, domain, port, inbound_tag, created_at)
		VALUES ('user-1', 'alice', 'bf000d23-0752-40b4-affe-68f7707a9661', 'node-1', 'example.com', 443, 'pulse-vless-443', '2024-01-01T00:00:00Z')
	`)
	if err != nil {
		t.Fatalf("insert legacy user error = %v", err)
	}
	if err := conn.Close(); err != nil {
		t.Fatalf("close legacy db error = %v", err)
	}

	db, err := Open(path)
	if err != nil {
		t.Fatalf("Open() with legacy schema error = %v", err)
	}
	defer db.Close()

	// 迁移后应能正常操作 users 和 user_inbounds
	_, err = db.UserStore().UpsertUser(users.User{
		ID:       "user-2",
		Username: "bob",
	})
	if err != nil {
		t.Fatalf("user upsert after migration error = %v", err)
	}

	// 旧数据迁移到 user_inbounds
	inbounds, err := db.UserStore().ListUserInboundsByNode("node-1")
	if err != nil {
		t.Fatalf("ListUserInboundsByNode after migration error = %v", err)
	}
	if len(inbounds) != 1 || inbounds[0].UserID != "user-1" {
		t.Fatalf("expected migrated inbound for user-1, got: %#v", inbounds)
	}
}
