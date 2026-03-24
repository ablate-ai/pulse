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
		`CREATE TABLE IF NOT EXISTS nodes (
			id TEXT PRIMARY KEY,
			name TEXT NOT NULL,
			base_url TEXT NOT NULL,
			auth_token TEXT NOT NULL
		);`,
		`CREATE TABLE IF NOT EXISTS users (
			id TEXT PRIMARY KEY,
			username TEXT NOT NULL,
			uuid TEXT NOT NULL,
			protocol TEXT NOT NULL,
			secret TEXT NOT NULL,
			method TEXT NOT NULL,
			enabled INTEGER NOT NULL,
			node_id TEXT NOT NULL,
			domain TEXT NOT NULL,
			port INTEGER NOT NULL,
			inbound_tag TEXT NOT NULL,
			traffic_limit_bytes INTEGER NOT NULL,
			upload_bytes INTEGER NOT NULL,
			download_bytes INTEGER NOT NULL,
			used_bytes INTEGER NOT NULL,
			synced_upload_bytes INTEGER NOT NULL,
			synced_download_bytes INTEGER NOT NULL,
			apply_count INTEGER NOT NULL,
			last_applied_at TEXT NOT NULL,
			created_at TEXT NOT NULL
		);`,
	}

	for _, stmt := range stmts {
		if _, err := db.conn.Exec(stmt); err != nil {
			return fmt.Errorf("init sqlite schema: %w", err)
		}
	}
	if err := db.migrateUsersTable(); err != nil {
		return err
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
	if _, ok := columns["enabled"]; !ok {
		ddl = append(ddl, `ALTER TABLE users ADD COLUMN enabled INTEGER NOT NULL DEFAULT 1`)
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

	for _, stmt := range ddl {
		if _, err := db.conn.Exec(stmt); err != nil {
			return fmt.Errorf("migrate users table: %w", err)
		}
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
