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

func (db *DB) SessionStore() *SessionStore {
	return &SessionStore{db: db.conn}
}

func (db *DB) SettingsStore() *SettingsStore {
	return &SettingsStore{db: db.conn}
}

func (db *DB) init() error {
	stmts := []string{
		// inbounds：节点上的监听入站，含服务端 TLS/Reality 配置
		`CREATE TABLE IF NOT EXISTS inbounds (
			id                    TEXT PRIMARY KEY,
			node_id               TEXT NOT NULL,
			protocol              TEXT NOT NULL,
			tag                   TEXT NOT NULL,
			port                  INTEGER NOT NULL,
			method                TEXT NOT NULL DEFAULT '',
			security              TEXT NOT NULL DEFAULT '',
			reality_private_key   TEXT NOT NULL DEFAULT '',
			reality_public_key    TEXT NOT NULL DEFAULT '',
			reality_handshake_addr TEXT NOT NULL DEFAULT '',
			reality_short_id      TEXT NOT NULL DEFAULT '',
			tls_cert_path         TEXT NOT NULL DEFAULT '',
			tls_key_path          TEXT NOT NULL DEFAULT ''
		);`,
		// hosts：客户端连接模板（地址 + TLS 客户端参数）
		`CREATE TABLE IF NOT EXISTS hosts (
			id                TEXT PRIMARY KEY,
			inbound_id        TEXT NOT NULL,
			remark            TEXT NOT NULL DEFAULT '',
			address           TEXT NOT NULL DEFAULT '',
			port              INTEGER NOT NULL DEFAULT 0,
			sni               TEXT NOT NULL DEFAULT '',
			host              TEXT NOT NULL DEFAULT '',
			path              TEXT NOT NULL DEFAULT '',
			security          TEXT NOT NULL DEFAULT 'none',
			alpn              TEXT NOT NULL DEFAULT '',
			fingerprint       TEXT NOT NULL DEFAULT '',
			allow_insecure    INTEGER NOT NULL DEFAULT 0,
			mux_enable        INTEGER NOT NULL DEFAULT 0,
			reality_public_key TEXT NOT NULL DEFAULT '',
			reality_short_id   TEXT NOT NULL DEFAULT '',
			reality_spider_x   TEXT NOT NULL DEFAULT ''
		);`,
		`CREATE TABLE IF NOT EXISTS nodes (
			id TEXT PRIMARY KEY,
			name TEXT NOT NULL,
			base_url TEXT NOT NULL,
			certificate TEXT NOT NULL DEFAULT ''
		);`,
		// users：用户身份 + 流量统计
		`CREATE TABLE IF NOT EXISTS users (
			id TEXT PRIMARY KEY,
			username TEXT NOT NULL,
			status TEXT NOT NULL DEFAULT 'active',
			note TEXT NOT NULL DEFAULT '',
			expire_at TEXT,
			data_limit_reset_strategy TEXT NOT NULL DEFAULT 'no_reset',
			traffic_limit_bytes INTEGER NOT NULL DEFAULT 0,
			upload_bytes INTEGER NOT NULL DEFAULT 0,
			download_bytes INTEGER NOT NULL DEFAULT 0,
			used_bytes INTEGER NOT NULL DEFAULT 0,
			on_hold_expire_at TEXT,
			last_traffic_reset_at TEXT,
			online_at TEXT,
			created_at TEXT NOT NULL
		);`,
		// user_inbounds：用户对节点的访问凭据（一条记录对应一个 user+node 对）
		`CREATE TABLE IF NOT EXISTS user_inbounds (
			id                   TEXT PRIMARY KEY,
			user_id              TEXT NOT NULL,
			node_id              TEXT NOT NULL,
			uuid                 TEXT NOT NULL DEFAULT '',
			secret               TEXT NOT NULL DEFAULT '',
			synced_upload_bytes  INTEGER NOT NULL DEFAULT 0,
			synced_download_bytes INTEGER NOT NULL DEFAULT 0,
			created_at           TEXT NOT NULL
		);`,
		`CREATE INDEX IF NOT EXISTS idx_user_inbounds_user_id ON user_inbounds(user_id);`,
		`CREATE INDEX IF NOT EXISTS idx_user_inbounds_node_id ON user_inbounds(node_id);`,
		// sessions：管理员登录 session，持久化以便服务重启后保持登录态
		`CREATE TABLE IF NOT EXISTS sessions (
			token      TEXT PRIMARY KEY,
			username   TEXT NOT NULL,
			created_at TEXT NOT NULL
		);`,
		// settings：系统配置 KV 表（如持久化管理员密码）
		`CREATE TABLE IF NOT EXISTS settings (
			key   TEXT PRIMARY KEY,
			value TEXT NOT NULL DEFAULT ''
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
	if err := db.migrateInboundsTable(); err != nil {
		return err
	}
	if err := db.migrateHostsTable(); err != nil {
		return err
	}
	if err := db.migrateUserInboundsTable(); err != nil {
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
	// auth_token 是旧版字段，新版使用证书鉴权，删除该列避免 NOT NULL 约束冲突
	if _, ok := columns["auth_token"]; ok {
		if _, err := db.conn.Exec(`ALTER TABLE nodes DROP COLUMN auth_token`); err != nil {
			return fmt.Errorf("migrate nodes drop auth_token: %w", err)
		}
	}
	return nil
}

// migrateUsersTable 将旧版 users 表迁移到新版（只含身份+流量字段）。
func (db *DB) migrateUsersTable() error {
	columns, err := db.tableColumns("users")
	if err != nil {
		return err
	}

	if _, ok := columns["status"]; !ok {
		if _, err := db.conn.Exec(`ALTER TABLE users ADD COLUMN status TEXT NOT NULL DEFAULT 'active'`); err != nil {
			return fmt.Errorf("migrate users add status: %w", err)
		}
	}

	// enabled → status 迁移
	if _, hasEnabled := columns["enabled"]; hasEnabled {
		if _, err := db.conn.Exec(`
			UPDATE users SET status = CASE WHEN enabled = 0 THEN 'disabled' ELSE 'active' END
			WHERE status = 'active' AND enabled = 0
		`); err != nil {
			return fmt.Errorf("migrate users enabled→status: %w", err)
		}
	}

	// 若 users 表仍有旧的 node_id 列，重建精简版
	if _, hasNodeID := columns["node_id"]; hasNodeID {
		return db.rebuildUsersTable(columns)
	}

	if _, ok := columns["sub_token"]; !ok {
		if _, err := db.conn.Exec(`ALTER TABLE users ADD COLUMN sub_token TEXT NOT NULL DEFAULT ''`); err != nil {
			return fmt.Errorf("migrate users add sub_token: %w", err)
		}
		if _, err := db.conn.Exec(`UPDATE users SET sub_token = lower(hex(randomblob(16))) WHERE sub_token = ''`); err != nil {
			return fmt.Errorf("migrate users generate sub_token: %w", err)
		}
	}

	if _, ok := columns["note"]; !ok {
		if _, err := db.conn.Exec(`ALTER TABLE users ADD COLUMN note TEXT NOT NULL DEFAULT ''`); err != nil {
			return fmt.Errorf("migrate users add note: %w", err)
		}
	}

	if _, ok := columns["online_at"]; !ok {
		if _, err := db.conn.Exec(`ALTER TABLE users ADD COLUMN online_at TEXT`); err != nil {
			return fmt.Errorf("migrate users add online_at: %w", err)
		}
	}

	if _, ok := columns["on_hold_expire_at"]; !ok {
		if _, err := db.conn.Exec(`ALTER TABLE users ADD COLUMN on_hold_expire_at TEXT`); err != nil {
			return fmt.Errorf("migrate users add on_hold_expire_at: %w", err)
		}
	}

	return nil
}

// migrateInboundsTable 添加新版 inbounds 表缺失的列（兼容旧数据库）。
func (db *DB) migrateInboundsTable() error {
	columns, err := db.tableColumns("inbounds")
	if err != nil {
		return err
	}
	additions := map[string]string{
		"method":                 `ALTER TABLE inbounds ADD COLUMN method TEXT NOT NULL DEFAULT ''`,
		"security":               `ALTER TABLE inbounds ADD COLUMN security TEXT NOT NULL DEFAULT ''`,
		"reality_private_key":    `ALTER TABLE inbounds ADD COLUMN reality_private_key TEXT NOT NULL DEFAULT ''`,
		"reality_public_key":     `ALTER TABLE inbounds ADD COLUMN reality_public_key TEXT NOT NULL DEFAULT ''`,
		"reality_handshake_addr": `ALTER TABLE inbounds ADD COLUMN reality_handshake_addr TEXT NOT NULL DEFAULT ''`,
		"reality_short_id":       `ALTER TABLE inbounds ADD COLUMN reality_short_id TEXT NOT NULL DEFAULT ''`,
		"tls_cert_path":          `ALTER TABLE inbounds ADD COLUMN tls_cert_path TEXT NOT NULL DEFAULT ''`,
		"tls_key_path":           `ALTER TABLE inbounds ADD COLUMN tls_key_path TEXT NOT NULL DEFAULT ''`,
	}
	for col, ddl := range additions {
		if _, ok := columns[col]; !ok {
			if _, err := db.conn.Exec(ddl); err != nil {
				return fmt.Errorf("migrate inbounds add %s: %w", col, err)
			}
		}
	}
	return nil
}

// migrateHostsTable 添加新版 hosts 表缺失的列（Reality 客户端参数）。
func (db *DB) migrateHostsTable() error {
	columns, err := db.tableColumns("hosts")
	if err != nil {
		return err
	}
	additions := map[string]string{
		"reality_public_key": `ALTER TABLE hosts ADD COLUMN reality_public_key TEXT NOT NULL DEFAULT ''`,
		"reality_short_id":   `ALTER TABLE hosts ADD COLUMN reality_short_id TEXT NOT NULL DEFAULT ''`,
		"reality_spider_x":   `ALTER TABLE hosts ADD COLUMN reality_spider_x TEXT NOT NULL DEFAULT ''`,
	}
	for col, ddl := range additions {
		if _, ok := columns[col]; !ok {
			if _, err := db.conn.Exec(ddl); err != nil {
				return fmt.Errorf("migrate hosts add %s: %w", col, err)
			}
		}
	}
	return nil
}

// migrateUserInboundsTable 将旧版 user_inbounds（含协议/TLS 字段）迁移到新版（只含凭据）。
// 幂等：若新版列结构已存在则跳过。
func (db *DB) migrateUserInboundsTable() error {
	columns, err := db.tableColumns("user_inbounds")
	if err != nil {
		return err
	}

	// 新版特征：没有 protocol 列
	if _, hasProtocol := columns["protocol"]; !hasProtocol {
		return nil
	}

	// 旧表还有 protocol 列，需要重建为简化版本
	if _, err := db.conn.Exec(`
		CREATE TABLE IF NOT EXISTS user_inbounds_slim (
			id                    TEXT PRIMARY KEY,
			user_id               TEXT NOT NULL,
			node_id               TEXT NOT NULL,
			uuid                  TEXT NOT NULL DEFAULT '',
			secret                TEXT NOT NULL DEFAULT '',
			synced_upload_bytes   INTEGER NOT NULL DEFAULT 0,
			synced_download_bytes INTEGER NOT NULL DEFAULT 0,
			created_at            TEXT NOT NULL
		)
	`); err != nil {
		return fmt.Errorf("create user_inbounds_slim: %w", err)
	}

	// 只保留每 (user_id, node_id) 对中 id 最小的记录（第一条），复制凭据
	if _, err := db.conn.Exec(`
		INSERT OR IGNORE INTO user_inbounds_slim (
			id, user_id, node_id, uuid, secret,
			synced_upload_bytes, synced_download_bytes, created_at
		)
		SELECT id, user_id, node_id,
		       COALESCE(uuid, ''), COALESCE(secret, ''),
		       COALESCE(synced_upload_bytes, 0), COALESCE(synced_download_bytes, 0),
		       created_at
		FROM user_inbounds ui
		WHERE ui.id = (
		    SELECT MIN(id) FROM user_inbounds
		    WHERE user_id = ui.user_id AND node_id = ui.node_id
		)
	`); err != nil {
		return fmt.Errorf("migrate user_inbounds to slim: %w", err)
	}

	if _, err := db.conn.Exec(`DROP TABLE user_inbounds`); err != nil {
		return fmt.Errorf("drop old user_inbounds: %w", err)
	}
	if _, err := db.conn.Exec(`ALTER TABLE user_inbounds_slim RENAME TO user_inbounds`); err != nil {
		return fmt.Errorf("rename user_inbounds_slim: %w", err)
	}

	// 重建索引
	if _, err := db.conn.Exec(`CREATE INDEX IF NOT EXISTS idx_user_inbounds_user_id ON user_inbounds(user_id)`); err != nil {
		return fmt.Errorf("recreate index user_id: %w", err)
	}
	if _, err := db.conn.Exec(`CREATE INDEX IF NOT EXISTS idx_user_inbounds_node_id ON user_inbounds(node_id)`); err != nil {
		return fmt.Errorf("recreate index node_id: %w", err)
	}

	return nil
}

// rebuildUsersTable 通过 CREATE/INSERT/DROP/RENAME 重建精简的 users 表。
func (db *DB) rebuildUsersTable(columns map[string]struct{}) error {
	_, hasLastReset := columns["last_traffic_reset_at"]
	_, hasTrafficLimit := columns["traffic_limit_bytes"]
	_, hasUpload := columns["upload_bytes"]
	_, hasDownload := columns["download_bytes"]
	_, hasUsed := columns["used_bytes"]

	if _, err := db.conn.Exec(`
		CREATE TABLE IF NOT EXISTS users_slim (
			id TEXT PRIMARY KEY,
			username TEXT NOT NULL,
			status TEXT NOT NULL DEFAULT 'active',
			note TEXT NOT NULL DEFAULT '',
			expire_at TEXT,
			data_limit_reset_strategy TEXT NOT NULL DEFAULT 'no_reset',
			traffic_limit_bytes INTEGER NOT NULL DEFAULT 0,
			upload_bytes INTEGER NOT NULL DEFAULT 0,
			download_bytes INTEGER NOT NULL DEFAULT 0,
			used_bytes INTEGER NOT NULL DEFAULT 0,
			on_hold_expire_at TEXT,
			last_traffic_reset_at TEXT,
			online_at TEXT,
			created_at TEXT NOT NULL,
			sub_token TEXT NOT NULL DEFAULT ''
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
			 last_traffic_reset_at, created_at, sub_token)
		SELECT
			id, username, status, %s, %s,
			%s, %s, %s, %s,
			%s, created_at, lower(hex(randomblob(16)))
		FROM users
	`, expireAt, resetStrategy, trafficLimit, uploadBytes, downloadBytes, usedBytes, lastReset)

	if _, err := db.conn.Exec(insertSQL); err != nil {
		return fmt.Errorf("copy users to users_slim: %w", err)
	}

	// 若旧 users 表有 node_id + uuid，则为每行创建 user_inbounds 记录
	if _, hasNodeID := columns["node_id"]; hasNodeID {
		if _, hasUUID := columns["uuid"]; hasUUID {
			_, hasSecret := columns["secret"]
			secretExpr := "''"
			if hasSecret {
				secretExpr = "COALESCE(secret, '')"
			}
			migrateSQL := fmt.Sprintf(`
				INSERT OR IGNORE INTO user_inbounds (id, user_id, node_id, uuid, secret, created_at)
				SELECT id, id, node_id,
				       COALESCE(uuid, ''),
				       %s,
				       created_at
				FROM users
			`, secretExpr)
			if _, err := db.conn.Exec(migrateSQL); err != nil {
				return fmt.Errorf("migrate users→user_inbounds: %w", err)
			}
		}
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
