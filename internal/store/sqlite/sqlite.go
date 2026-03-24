package sqlite

import (
	"database/sql"
	"fmt"
	"strings"

	_ "modernc.org/sqlite"
)

type DB struct {
	conn *sql.DB
}

func Open(path string) (*DB, error) {
	conn, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, fmt.Errorf("open sqlite: %w", err)
	}

	db := &DB{conn: conn}
	if err := db.init(); err != nil {
		conn.Close()
		return nil, err
	}
	return db, nil
}

func (db *DB) Close() error {
	return db.conn.Close()
}

func (db *DB) NodeStore() *NodeStore {
	return &NodeStore{db: db.conn}
}

func (db *DB) UserStore() *UserStore {
	return &UserStore{db: db.conn}
}


func (db *DB) init() error {
	stmts := []string{
		`CREATE TABLE IF NOT EXISTS inbounds (
			id       TEXT PRIMARY KEY,
			node_id  TEXT NOT NULL,
			protocol TEXT NOT NULL,
			tag      TEXT NOT NULL,
			port     INTEGER NOT NULL
		);`,
		`CREATE TABLE IF NOT EXISTS hosts (
			id             TEXT PRIMARY KEY,
			inbound_id     TEXT NOT NULL,
			remark         TEXT NOT NULL DEFAULT '',
			address        TEXT NOT NULL DEFAULT '',
			port           INTEGER NOT NULL DEFAULT 0,
			sni            TEXT NOT NULL DEFAULT '',
			host           TEXT NOT NULL DEFAULT '',
			path           TEXT NOT NULL DEFAULT '',
			security       TEXT NOT NULL DEFAULT 'none',
			alpn           TEXT NOT NULL DEFAULT '',
			fingerprint    TEXT NOT NULL DEFAULT '',
			allow_insecure INTEGER NOT NULL DEFAULT 0,
			mux_enable     INTEGER NOT NULL DEFAULT 0
		);`,
		`CREATE TABLE IF NOT EXISTS nodes (
			id TEXT PRIMARY KEY,
			name TEXT NOT NULL,
			base_url TEXT NOT NULL,
			certificate TEXT NOT NULL DEFAULT ''
		);`,
		`CREATE TABLE IF NOT EXISTS users (
			id TEXT PRIMARY KEY,
			username TEXT NOT NULL,
			uuid TEXT NOT NULL,
			protocol TEXT NOT NULL DEFAULT 'vless',
			secret TEXT NOT NULL DEFAULT '',
			method TEXT NOT NULL DEFAULT 'aes-128-gcm',
			status TEXT NOT NULL DEFAULT 'active',
			expire_at TEXT,
			data_limit_reset_strategy TEXT NOT NULL DEFAULT 'no_reset',
			node_id TEXT NOT NULL,
			domain TEXT NOT NULL,
			port INTEGER NOT NULL,
			inbound_tag TEXT NOT NULL,
			traffic_limit_bytes INTEGER NOT NULL DEFAULT 0,
			upload_bytes INTEGER NOT NULL DEFAULT 0,
			download_bytes INTEGER NOT NULL DEFAULT 0,
			used_bytes INTEGER NOT NULL DEFAULT 0,
			synced_upload_bytes INTEGER NOT NULL DEFAULT 0,
			synced_download_bytes INTEGER NOT NULL DEFAULT 0,
			apply_count INTEGER NOT NULL DEFAULT 0,
			last_applied_at TEXT NOT NULL DEFAULT '',
			last_traffic_reset_at TEXT,
			created_at TEXT NOT NULL
		);`,
	}

	for _, stmt := range stmts {
		if _, err := db.conn.Exec(stmt); err != nil {
			return fmt.Errorf("init sqlite schema: %w", err)
		}
	}
	if err := db.migrateNodesTable(); err != nil {
		return err
	}
	if err := db.migrateUsersTable(); err != nil {
		return err
	}
	return nil
}

func (db *DB) migrateNodesTable() error {
	columns, err := db.tableColumns("nodes")
	if err != nil {
		return err
	}

	if _, ok := columns["certificate"]; !ok {
		if _, err := db.conn.Exec(`ALTER TABLE nodes ADD COLUMN certificate TEXT NOT NULL DEFAULT ''`); err != nil {
			return fmt.Errorf("migrate nodes table: %w", err)
		}
	}
	return nil
}

func (db *DB) migrateUsersTable() error {
	columns, err := db.tableColumns("users")
	if err != nil {
		return err
	}

	ddl := []string{}
	if _, ok := columns["protocol"]; !ok {
		ddl = append(ddl, `ALTER TABLE users ADD COLUMN protocol TEXT NOT NULL DEFAULT 'vless'`)
	}
	if _, ok := columns["secret"]; !ok {
		ddl = append(ddl, `ALTER TABLE users ADD COLUMN secret TEXT NOT NULL DEFAULT ''`)
	}
	if _, ok := columns["method"]; !ok {
		ddl = append(ddl, `ALTER TABLE users ADD COLUMN method TEXT NOT NULL DEFAULT 'aes-128-gcm'`)
	}
	// 新增 status 列，从旧 enabled 列迁移数据
	if _, ok := columns["status"]; !ok {
		ddl = append(ddl, `ALTER TABLE users ADD COLUMN status TEXT NOT NULL DEFAULT 'active'`)
	}
	if _, ok := columns["expire_at"]; !ok {
		ddl = append(ddl, `ALTER TABLE users ADD COLUMN expire_at TEXT`)
	}
	if _, ok := columns["data_limit_reset_strategy"]; !ok {
		ddl = append(ddl, `ALTER TABLE users ADD COLUMN data_limit_reset_strategy TEXT NOT NULL DEFAULT 'no_reset'`)
	}
	if _, ok := columns["traffic_limit_bytes"]; !ok {
		ddl = append(ddl, `ALTER TABLE users ADD COLUMN traffic_limit_bytes INTEGER NOT NULL DEFAULT 0`)
	}
	if _, ok := columns["upload_bytes"]; !ok {
		ddl = append(ddl, `ALTER TABLE users ADD COLUMN upload_bytes INTEGER NOT NULL DEFAULT 0`)
	}
	if _, ok := columns["download_bytes"]; !ok {
		ddl = append(ddl, `ALTER TABLE users ADD COLUMN download_bytes INTEGER NOT NULL DEFAULT 0`)
	}
	if _, ok := columns["used_bytes"]; !ok {
		ddl = append(ddl, `ALTER TABLE users ADD COLUMN used_bytes INTEGER NOT NULL DEFAULT 0`)
	}
	if _, ok := columns["synced_upload_bytes"]; !ok {
		ddl = append(ddl, `ALTER TABLE users ADD COLUMN synced_upload_bytes INTEGER NOT NULL DEFAULT 0`)
	}
	if _, ok := columns["synced_download_bytes"]; !ok {
		ddl = append(ddl, `ALTER TABLE users ADD COLUMN synced_download_bytes INTEGER NOT NULL DEFAULT 0`)
	}
	if _, ok := columns["apply_count"]; !ok {
		ddl = append(ddl, `ALTER TABLE users ADD COLUMN apply_count INTEGER NOT NULL DEFAULT 0`)
	}
	if _, ok := columns["last_applied_at"]; !ok {
		ddl = append(ddl, `ALTER TABLE users ADD COLUMN last_applied_at TEXT NOT NULL DEFAULT ''`)
	}
	if _, ok := columns["last_traffic_reset_at"]; !ok {
		ddl = append(ddl, `ALTER TABLE users ADD COLUMN last_traffic_reset_at TEXT`)
	}

	for _, stmt := range ddl {
		if _, err := db.conn.Exec(stmt); err != nil {
			return fmt.Errorf("migrate users table: %w", err)
		}
	}

	// 将旧 enabled 列数据迁移到 status 列，然后重建表去掉 enabled 列
	if _, hasEnabled := columns["enabled"]; hasEnabled {
		if _, err := db.conn.Exec(`
			UPDATE users SET status = CASE WHEN enabled = 0 THEN 'disabled' ELSE 'active' END
			WHERE status = 'active' AND enabled = 0
		`); err != nil {
			return fmt.Errorf("migrate users enabled→status: %w", err)
		}
		if err := db.dropUsersEnabledColumn(); err != nil {
			return err
		}
	}

	return nil
}

// dropUsersEnabledColumn 通过重建表的方式移除旧的 enabled 列。
func (db *DB) dropUsersEnabledColumn() error {
	_, err := db.conn.Exec(`
		CREATE TABLE IF NOT EXISTS users_new (
			id TEXT PRIMARY KEY,
			username TEXT NOT NULL,
			uuid TEXT NOT NULL,
			protocol TEXT NOT NULL DEFAULT 'vless',
			secret TEXT NOT NULL DEFAULT '',
			method TEXT NOT NULL DEFAULT 'aes-128-gcm',
			status TEXT NOT NULL DEFAULT 'active',
			expire_at TEXT,
			data_limit_reset_strategy TEXT NOT NULL DEFAULT 'no_reset',
			node_id TEXT NOT NULL,
			domain TEXT NOT NULL,
			port INTEGER NOT NULL,
			inbound_tag TEXT NOT NULL,
			traffic_limit_bytes INTEGER NOT NULL DEFAULT 0,
			upload_bytes INTEGER NOT NULL DEFAULT 0,
			download_bytes INTEGER NOT NULL DEFAULT 0,
			used_bytes INTEGER NOT NULL DEFAULT 0,
			synced_upload_bytes INTEGER NOT NULL DEFAULT 0,
			synced_download_bytes INTEGER NOT NULL DEFAULT 0,
			apply_count INTEGER NOT NULL DEFAULT 0,
			last_applied_at TEXT NOT NULL DEFAULT '',
			last_traffic_reset_at TEXT,
			created_at TEXT NOT NULL
		)
	`)
	if err != nil {
		return fmt.Errorf("create users_new: %w", err)
	}
	if _, err := db.conn.Exec(`
		INSERT INTO users_new SELECT
			id, username, uuid, protocol, secret, method,
			status, expire_at, data_limit_reset_strategy,
			node_id, domain, port, inbound_tag,
			traffic_limit_bytes, upload_bytes, download_bytes, used_bytes,
			synced_upload_bytes, synced_download_bytes,
			apply_count, last_applied_at, last_traffic_reset_at, created_at
		FROM users
	`); err != nil {
		return fmt.Errorf("copy users to users_new: %w", err)
	}
	if _, err := db.conn.Exec(`DROP TABLE users`); err != nil {
		return fmt.Errorf("drop old users table: %w", err)
	}
	if _, err := db.conn.Exec(`ALTER TABLE users_new RENAME TO users`); err != nil {
		return fmt.Errorf("rename users_new to users: %w", err)
	}
	return nil
}

func (db *DB) tableColumns(name string) (map[string]struct{}, error) {
	rows, err := db.conn.Query("PRAGMA table_info(" + name + ")")
	if err != nil {
		return nil, fmt.Errorf("query table info for %s: %w", name, err)
	}
	defer rows.Close()

	columns := make(map[string]struct{})
	for rows.Next() {
		var cid int
		var columnName string
		var columnType string
		var notNull int
		var defaultValue sql.NullString
		var pk int
		if err := rows.Scan(&cid, &columnName, &columnType, &notNull, &defaultValue, &pk); err != nil {
			return nil, fmt.Errorf("scan table info for %s: %w", name, err)
		}
		columns[strings.ToLower(columnName)] = struct{}{}
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate table info for %s: %w", name, err)
	}
	return columns, nil
}
