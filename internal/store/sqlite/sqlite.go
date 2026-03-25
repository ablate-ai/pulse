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
		// users 表：只含身份+流量字段
		`CREATE TABLE IF NOT EXISTS users (
			id TEXT PRIMARY KEY,
			username TEXT NOT NULL,
			status TEXT NOT NULL DEFAULT 'active',
			expire_at TEXT,
			data_limit_reset_strategy TEXT NOT NULL DEFAULT 'no_reset',
			traffic_limit_bytes INTEGER NOT NULL DEFAULT 0,
			upload_bytes INTEGER NOT NULL DEFAULT 0,
			download_bytes INTEGER NOT NULL DEFAULT 0,
			used_bytes INTEGER NOT NULL DEFAULT 0,
			last_traffic_reset_at TEXT,
			created_at TEXT NOT NULL
		);`,
		// user_inbounds 表：协议+节点+连接配置
		`CREATE TABLE IF NOT EXISTS user_inbounds (
			id TEXT PRIMARY KEY,
			user_id TEXT NOT NULL,
			node_id TEXT NOT NULL,
			protocol TEXT NOT NULL DEFAULT 'vless',
			uuid TEXT NOT NULL DEFAULT '',
			secret TEXT NOT NULL DEFAULT '',
			method TEXT NOT NULL DEFAULT 'aes-128-gcm',
			security TEXT NOT NULL DEFAULT '',
			flow TEXT NOT NULL DEFAULT '',
			sni TEXT NOT NULL DEFAULT '',
			fingerprint TEXT NOT NULL DEFAULT '',
			reality_public_key TEXT NOT NULL DEFAULT '',
			reality_short_id TEXT NOT NULL DEFAULT '',
			reality_spider_x TEXT NOT NULL DEFAULT '',
			reality_private_key TEXT NOT NULL DEFAULT '',
			reality_handshake_addr TEXT NOT NULL DEFAULT '',
			domain TEXT NOT NULL DEFAULT '',
			port INTEGER NOT NULL DEFAULT 0,
			inbound_tag TEXT NOT NULL DEFAULT '',
			synced_upload_bytes INTEGER NOT NULL DEFAULT 0,
			synced_download_bytes INTEGER NOT NULL DEFAULT 0,
			apply_count INTEGER NOT NULL DEFAULT 0,
			last_applied_at TEXT NOT NULL DEFAULT '',
			created_at TEXT NOT NULL
		);`,
		`CREATE INDEX IF NOT EXISTS idx_user_inbounds_user_id ON user_inbounds(user_id);`,
		`CREATE INDEX IF NOT EXISTS idx_user_inbounds_node_id ON user_inbounds(node_id);`,
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
	if err := db.migrateUsersToInbounds(); err != nil {
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

// migrateUsersTable 将旧版 users 表迁移到新版（只含身份+流量字段）。
// 若 users 表还有旧的协议/节点字段，则先补齐缺失列再做结构迁移。
func (db *DB) migrateUsersTable() error {
	columns, err := db.tableColumns("users")
	if err != nil {
		return err
	}

	// 若旧表还没有 status 列，补加（兼容非常旧的数据库）
	if _, ok := columns["status"]; !ok {
		if _, err := db.conn.Exec(`ALTER TABLE users ADD COLUMN status TEXT NOT NULL DEFAULT 'active'`); err != nil {
			return fmt.Errorf("migrate users add status: %w", err)
		}
	}

	// 处理旧的 enabled → status 迁移
	if _, hasEnabled := columns["enabled"]; hasEnabled {
		if _, err := db.conn.Exec(`
			UPDATE users SET status = CASE WHEN enabled = 0 THEN 'disabled' ELSE 'active' END
			WHERE status = 'active' AND enabled = 0
		`); err != nil {
			return fmt.Errorf("migrate users enabled→status: %w", err)
		}
	}

	return nil
}

// migrateUsersToInbounds 将旧 users 表中的协议/节点字段迁移到 user_inbounds 表，
// 然后重建精简的 users 表。幂等：已有 user_inbounds 数据则跳过。
func (db *DB) migrateUsersToInbounds() error {
	// 检查 user_inbounds 表是否已有数据
	var count int
	if err := db.conn.QueryRow(`SELECT COUNT(*) FROM user_inbounds`).Scan(&count); err != nil {
		return fmt.Errorf("check user_inbounds count: %w", err)
	}

	columns, err := db.tableColumns("users")
	if err != nil {
		return err
	}

	// 检查旧的 node_id 列是否存在
	_, hasNodeID := columns["node_id"]

	// 若旧表已无 node_id 列，说明已完成迁移，直接跳过
	if !hasNodeID {
		return nil
	}

	// 若 user_inbounds 还没数据，把旧 users 数据迁入
	if count == 0 {
		// 补全旧表可能缺失的列，避免 INSERT 出错
		legacyColDefaults := map[string]string{
			"uuid":                   `ALTER TABLE users ADD COLUMN uuid TEXT NOT NULL DEFAULT ''`,
			"protocol":               `ALTER TABLE users ADD COLUMN protocol TEXT NOT NULL DEFAULT 'vless'`,
			"secret":                 `ALTER TABLE users ADD COLUMN secret TEXT NOT NULL DEFAULT ''`,
			"method":                 `ALTER TABLE users ADD COLUMN method TEXT NOT NULL DEFAULT 'aes-128-gcm'`,
			"security":               `ALTER TABLE users ADD COLUMN security TEXT NOT NULL DEFAULT ''`,
			"flow":                   `ALTER TABLE users ADD COLUMN flow TEXT NOT NULL DEFAULT ''`,
			"sni":                    `ALTER TABLE users ADD COLUMN sni TEXT NOT NULL DEFAULT ''`,
			"fingerprint":            `ALTER TABLE users ADD COLUMN fingerprint TEXT NOT NULL DEFAULT ''`,
			"reality_public_key":     `ALTER TABLE users ADD COLUMN reality_public_key TEXT NOT NULL DEFAULT ''`,
			"reality_short_id":       `ALTER TABLE users ADD COLUMN reality_short_id TEXT NOT NULL DEFAULT ''`,
			"reality_spider_x":       `ALTER TABLE users ADD COLUMN reality_spider_x TEXT NOT NULL DEFAULT ''`,
			"reality_private_key":    `ALTER TABLE users ADD COLUMN reality_private_key TEXT NOT NULL DEFAULT ''`,
			"reality_handshake_addr": `ALTER TABLE users ADD COLUMN reality_handshake_addr TEXT NOT NULL DEFAULT ''`,
			"domain":                 `ALTER TABLE users ADD COLUMN domain TEXT NOT NULL DEFAULT ''`,
			"port":                   `ALTER TABLE users ADD COLUMN port INTEGER NOT NULL DEFAULT 0`,
			"inbound_tag":            `ALTER TABLE users ADD COLUMN inbound_tag TEXT NOT NULL DEFAULT ''`,
			"synced_upload_bytes":    `ALTER TABLE users ADD COLUMN synced_upload_bytes INTEGER NOT NULL DEFAULT 0`,
			"synced_download_bytes":  `ALTER TABLE users ADD COLUMN synced_download_bytes INTEGER NOT NULL DEFAULT 0`,
			"apply_count":            `ALTER TABLE users ADD COLUMN apply_count INTEGER NOT NULL DEFAULT 0`,
			"last_applied_at":        `ALTER TABLE users ADD COLUMN last_applied_at TEXT NOT NULL DEFAULT ''`,
		}
		// 重新查询，因为上面的 migrateUsersTable 可能已经加了一些列
		columns, err = db.tableColumns("users")
		if err != nil {
			return err
		}
		for col, ddl := range legacyColDefaults {
			if _, ok := columns[col]; !ok {
				if _, err := db.conn.Exec(ddl); err != nil {
					return fmt.Errorf("migrate users add column %s: %w", col, err)
				}
			}
		}

		// 将旧 users 数据插入 user_inbounds
		if _, err := db.conn.Exec(`
			INSERT INTO user_inbounds (
				id, user_id, node_id, protocol, uuid, secret, method,
				security, flow, sni, fingerprint,
				reality_public_key, reality_short_id, reality_spider_x,
				reality_private_key, reality_handshake_addr,
				domain, port, inbound_tag,
				synced_upload_bytes, synced_download_bytes,
				apply_count, last_applied_at, created_at
			)
			SELECT
				id || '-ib0', id, node_id, protocol, uuid, secret, method,
				COALESCE(security, ''), COALESCE(flow, ''), COALESCE(sni, ''),
				COALESCE(fingerprint, ''),
				COALESCE(reality_public_key, ''), COALESCE(reality_short_id, ''),
				COALESCE(reality_spider_x, ''),
				COALESCE(reality_private_key, ''), COALESCE(reality_handshake_addr, ''),
				domain, port, inbound_tag,
				synced_upload_bytes, synced_download_bytes,
				apply_count, last_applied_at, created_at
			FROM users
			WHERE node_id IS NOT NULL AND node_id != ''
		`); err != nil {
			return fmt.Errorf("migrate users to user_inbounds: %w", err)
		}
	}

	// 重建精简的 users 表（去掉协议/节点字段）
	return db.rebuildUsersTable(columns)
}

// rebuildUsersTable 通过 CREATE/INSERT/DROP/RENAME 重建精简的 users 表。
func (db *DB) rebuildUsersTable(columns map[string]struct{}) error {
	// 检查是否有 last_traffic_reset_at 列
	_, hasLastReset := columns["last_traffic_reset_at"]
	// 检查是否有 traffic_limit_bytes 列
	_, hasTrafficLimit := columns["traffic_limit_bytes"]
	_, hasUpload := columns["upload_bytes"]
	_, hasDownload := columns["download_bytes"]
	_, hasUsed := columns["used_bytes"]

	if _, err := db.conn.Exec(`
		CREATE TABLE IF NOT EXISTS users_slim (
			id TEXT PRIMARY KEY,
			username TEXT NOT NULL,
			status TEXT NOT NULL DEFAULT 'active',
			expire_at TEXT,
			data_limit_reset_strategy TEXT NOT NULL DEFAULT 'no_reset',
			traffic_limit_bytes INTEGER NOT NULL DEFAULT 0,
			upload_bytes INTEGER NOT NULL DEFAULT 0,
			download_bytes INTEGER NOT NULL DEFAULT 0,
			used_bytes INTEGER NOT NULL DEFAULT 0,
			last_traffic_reset_at TEXT,
			created_at TEXT NOT NULL
		)
	`); err != nil {
		return fmt.Errorf("create users_slim: %w", err)
	}

	trafficLimit := "0"
	if hasTrafficLimit {
		trafficLimit = "traffic_limit_bytes"
	}
	uploadBytes := "0"
	if hasUpload {
		uploadBytes = "upload_bytes"
	}
	downloadBytes := "0"
	if hasDownload {
		downloadBytes = "download_bytes"
	}
	usedBytes := "0"
	if hasUsed {
		usedBytes = "used_bytes"
	}
	lastReset := "NULL"
	if hasLastReset {
		lastReset = "last_traffic_reset_at"
	}

	expireAt := "NULL"
	if _, hasExpireAt := columns["expire_at"]; hasExpireAt {
		expireAt = "expire_at"
	}
	resetStrategy := "'no_reset'"
	if _, hasResetStrategy := columns["data_limit_reset_strategy"]; hasResetStrategy {
		resetStrategy = "data_limit_reset_strategy"
	}

	insertSQL := fmt.Sprintf(`
		INSERT INTO users_slim
			(id, username, status, expire_at, data_limit_reset_strategy,
			 traffic_limit_bytes, upload_bytes, download_bytes, used_bytes,
			 last_traffic_reset_at, created_at)
		SELECT
			id, username, status, %s, %s,
			%s, %s, %s, %s,
			%s, created_at
		FROM users
	`, expireAt, resetStrategy, trafficLimit, uploadBytes, downloadBytes, usedBytes, lastReset)

	if _, err := db.conn.Exec(insertSQL); err != nil {
		return fmt.Errorf("copy users to users_slim: %w", err)
	}
	if _, err := db.conn.Exec(`DROP TABLE users`); err != nil {
		return fmt.Errorf("drop old users table: %w", err)
	}
	if _, err := db.conn.Exec(`ALTER TABLE users_slim RENAME TO users`); err != nil {
		return fmt.Errorf("rename users_slim to users: %w", err)
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
