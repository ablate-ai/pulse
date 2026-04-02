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
			password              TEXT NOT NULL DEFAULT '',
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
		// outbounds：独立的出口代理配置
		`CREATE TABLE IF NOT EXISTS outbounds (
			id       TEXT PRIMARY KEY,
			name     TEXT NOT NULL DEFAULT '',
			protocol TEXT NOT NULL DEFAULT 'socks5',
			server   TEXT NOT NULL DEFAULT '',
			username TEXT NOT NULL DEFAULT '',
			password TEXT NOT NULL DEFAULT ''
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
			raw_upload_bytes INTEGER NOT NULL DEFAULT 0,
			raw_download_bytes INTEGER NOT NULL DEFAULT 0,
			on_hold_expire_at TEXT,
			last_traffic_reset_at TEXT,
			online_at TEXT,
			connections INTEGER NOT NULL DEFAULT 0,
			devices INTEGER NOT NULL DEFAULT 0,
			created_at TEXT NOT NULL
		);`,
		// user_inbounds：用户对具体 inbound 的访问凭据（一条记录对应一个 user+inbound 对）
		// 注：synced_upload_bytes / synced_download_bytes 为旧版 cursor 设计遗留字段，
		// 当前使用 V2Ray Stats reset=true 获取 delta，不再需要。
		// 新建表不再包含这两列，旧表中的列保留不删除（兼容性）。
		`CREATE TABLE IF NOT EXISTS user_inbounds (
			id                   TEXT PRIMARY KEY,
			user_id              TEXT NOT NULL,
			inbound_id           TEXT NOT NULL DEFAULT '',
			node_id              TEXT NOT NULL DEFAULT '',
			uuid                 TEXT NOT NULL DEFAULT '',
			secret               TEXT NOT NULL DEFAULT '',
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
		// node_daily_usage：节点按天流量 delta，供历史趋势图使用
		`CREATE TABLE IF NOT EXISTS node_daily_usage (
			node_id        TEXT NOT NULL,
			date           TEXT NOT NULL,
			upload_bytes   INTEGER NOT NULL DEFAULT 0,
			download_bytes INTEGER NOT NULL DEFAULT 0,
			PRIMARY KEY (node_id, date)
		);`,
		// sub_access_logs：记录 /sub/:token 的访问记录
		`CREATE TABLE IF NOT EXISTS sub_access_logs (
			id          INTEGER PRIMARY KEY AUTOINCREMENT,
			user_id     TEXT NOT NULL,
			ip          TEXT NOT NULL DEFAULT '',
			user_agent  TEXT NOT NULL DEFAULT '',
			accessed_at TEXT NOT NULL
		);`,
		`CREATE INDEX IF NOT EXISTS idx_sub_access_logs_user_id ON sub_access_logs(user_id);`,
		// node_speedtest：节点测速结果，按 node_id 唯一
		`CREATE TABLE IF NOT EXISTS node_speedtest (
			node_id   TEXT PRIMARY KEY,
			down_bps  INTEGER NOT NULL DEFAULT 0,
			up_bps    INTEGER NOT NULL DEFAULT 0,
			tested_at TEXT NOT NULL DEFAULT ''
		);`,
		// node_check_results：节点解锁检测结果，按 (node_id, service) 唯一存储
		`CREATE TABLE IF NOT EXISTS node_check_results (
			node_id    TEXT NOT NULL,
			service    TEXT NOT NULL,
			unlocked   INTEGER NOT NULL DEFAULT 0,
			region     TEXT NOT NULL DEFAULT '',
			checked_at TEXT NOT NULL,
			PRIMARY KEY (node_id, service)
		);`,
		// user_node_daily_usage：用户在各节点的按天流量，用于节点用量分析
		`CREATE TABLE IF NOT EXISTS user_node_daily_usage (
			user_id        TEXT NOT NULL,
			node_id        TEXT NOT NULL,
			date           TEXT NOT NULL,
			upload_bytes   INTEGER NOT NULL DEFAULT 0,
			download_bytes INTEGER NOT NULL DEFAULT 0,
			PRIMARY KEY (user_id, node_id, date)
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
	if err := db.migrateOutboundsTable(); err != nil {
		return err
	}
	// inbound_id 索引必须在迁移完成后创建，避免旧库中该列尚不存在时报错
	if _, err := db.conn.Exec(`CREATE INDEX IF NOT EXISTS idx_user_inbounds_inbound_id ON user_inbounds(inbound_id)`); err != nil {
		return fmt.Errorf("init sqlite schema: create idx_user_inbounds_inbound_id: %w", err)
	}
	return nil
}

func (db *DB) migrateOutboundsTable() error {
	columns, err := db.tableColumns("outbounds")
	if err != nil {
		return err
	}
	additions := map[string]string{
		"method":      `ALTER TABLE outbounds ADD COLUMN method TEXT NOT NULL DEFAULT ''`,
		"uuid":        `ALTER TABLE outbounds ADD COLUMN uuid TEXT NOT NULL DEFAULT ''`,
		"sni":         `ALTER TABLE outbounds ADD COLUMN sni TEXT NOT NULL DEFAULT ''`,
		"public_key":  `ALTER TABLE outbounds ADD COLUMN public_key TEXT NOT NULL DEFAULT ''`,
		"short_id":    `ALTER TABLE outbounds ADD COLUMN short_id TEXT NOT NULL DEFAULT ''`,
		"fingerprint": `ALTER TABLE outbounds ADD COLUMN fingerprint TEXT NOT NULL DEFAULT ''`,
	}
	for col, ddl := range additions {
		if _, ok := columns[col]; !ok {
			if _, err := db.conn.Exec(ddl); err != nil {
				return fmt.Errorf("migrate outbounds add %s: %w", col, err)
			}
		}
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
	if _, ok := columns["upload_bytes"]; !ok {
		if _, err := db.conn.Exec(`ALTER TABLE nodes ADD COLUMN upload_bytes INTEGER NOT NULL DEFAULT 0`); err != nil {
			return fmt.Errorf("migrate nodes add upload_bytes: %w", err)
		}
	}
	if _, ok := columns["download_bytes"]; !ok {
		if _, err := db.conn.Exec(`ALTER TABLE nodes ADD COLUMN download_bytes INTEGER NOT NULL DEFAULT 0`); err != nil {
			return fmt.Errorf("migrate nodes add download_bytes: %w", err)
		}
	}
	if _, ok := columns["caddy_acme_email"]; !ok {
		if _, err := db.conn.Exec(`ALTER TABLE nodes ADD COLUMN caddy_acme_email TEXT NOT NULL DEFAULT ''`); err != nil {
			return fmt.Errorf("migrate nodes add caddy_acme_email: %w", err)
		}
	}
	if _, ok := columns["caddy_panel_domain"]; !ok {
		if _, err := db.conn.Exec(`ALTER TABLE nodes ADD COLUMN caddy_panel_domain TEXT NOT NULL DEFAULT ''`); err != nil {
			return fmt.Errorf("migrate nodes add caddy_panel_domain: %w", err)
		}
	}
	if _, ok := columns["caddy_extra_proxies"]; !ok {
		if _, err := db.conn.Exec(`ALTER TABLE nodes ADD COLUMN caddy_extra_proxies TEXT NOT NULL DEFAULT ''`); err != nil {
			return fmt.Errorf("migrate nodes add caddy_extra_proxies: %w", err)
		}
	}
	if _, ok := columns["caddy_enabled"]; !ok {
		if _, err := db.conn.Exec(`ALTER TABLE nodes ADD COLUMN caddy_enabled INTEGER NOT NULL DEFAULT 0`); err != nil {
			return fmt.Errorf("migrate nodes add caddy_enabled: %w", err)
		}
	}
	if _, ok := columns["traffic_rate"]; !ok {
		if _, err := db.conn.Exec(`ALTER TABLE nodes ADD COLUMN traffic_rate REAL NOT NULL DEFAULT 1.0`); err != nil {
			return fmt.Errorf("migrate nodes add traffic_rate: %w", err)
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

	additions := map[string]string{
		"note":               `ALTER TABLE users ADD COLUMN note TEXT NOT NULL DEFAULT ''`,
		"online_at":          `ALTER TABLE users ADD COLUMN online_at TEXT`,
		"on_hold_expire_at":  `ALTER TABLE users ADD COLUMN on_hold_expire_at TEXT`,
		"connections":        `ALTER TABLE users ADD COLUMN connections INTEGER NOT NULL DEFAULT 0`,
		"devices":            `ALTER TABLE users ADD COLUMN devices INTEGER NOT NULL DEFAULT 0`,
		"raw_upload_bytes":   `ALTER TABLE users ADD COLUMN raw_upload_bytes INTEGER NOT NULL DEFAULT 0`,
		"raw_download_bytes": `ALTER TABLE users ADD COLUMN raw_download_bytes INTEGER NOT NULL DEFAULT 0`,
	}
	for col, ddl := range additions {
		if _, ok := columns[col]; !ok {
			if _, err := db.conn.Exec(ddl); err != nil {
				return fmt.Errorf("migrate users add %s: %w", col, err)
			}
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
		"password":               `ALTER TABLE inbounds ADD COLUMN password TEXT NOT NULL DEFAULT ''`,
		"security":               `ALTER TABLE inbounds ADD COLUMN security TEXT NOT NULL DEFAULT ''`,
		"reality_private_key":    `ALTER TABLE inbounds ADD COLUMN reality_private_key TEXT NOT NULL DEFAULT ''`,
		"reality_public_key":     `ALTER TABLE inbounds ADD COLUMN reality_public_key TEXT NOT NULL DEFAULT ''`,
		"reality_handshake_addr": `ALTER TABLE inbounds ADD COLUMN reality_handshake_addr TEXT NOT NULL DEFAULT ''`,
		"reality_short_id":       `ALTER TABLE inbounds ADD COLUMN reality_short_id TEXT NOT NULL DEFAULT ''`,
		"tls_cert_path":          `ALTER TABLE inbounds ADD COLUMN tls_cert_path TEXT NOT NULL DEFAULT ''`,
		"tls_key_path":           `ALTER TABLE inbounds ADD COLUMN tls_key_path TEXT NOT NULL DEFAULT ''`,
		"outbound_id":            `ALTER TABLE inbounds ADD COLUMN outbound_id TEXT NOT NULL DEFAULT ''`,
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

	// 旧版含 protocol 列：需要重建
	if _, hasProtocol := columns["protocol"]; !hasProtocol {
		// 无旧版 protocol 列，跳到 inbound_id 迁移
		return db.migrateUserInboundsAddInboundID(columns)
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

	// 重建后的表没有 inbound_id，继续执行 inbound_id 迁移
	newCols, err := db.tableColumns("user_inbounds")
	if err != nil {
		return err
	}
	return db.migrateUserInboundsAddInboundID(newCols)
}

// migrateUserInboundsAddInboundID 为 user_inbounds 添加 inbound_id 列，
// 并将旧版 node 级别记录扩展为 inbound 级别记录（幂等）。
func (db *DB) migrateUserInboundsAddInboundID(columns map[string]struct{}) error {
	if _, hasInboundID := columns["inbound_id"]; hasInboundID {
		return nil // 已迁移
	}

	if _, err := db.conn.Exec(`ALTER TABLE user_inbounds ADD COLUMN inbound_id TEXT NOT NULL DEFAULT ''`); err != nil {
		return fmt.Errorf("migrate user_inbounds add inbound_id: %w", err)
	}
	if _, err := db.conn.Exec(`CREATE INDEX IF NOT EXISTS idx_user_inbounds_inbound_id ON user_inbounds(inbound_id)`); err != nil {
		return fmt.Errorf("migrate user_inbounds add inbound_id index: %w", err)
	}

	// 将旧版 (user_id, node_id) 记录扩展为 (user_id, inbound_id) 记录
	// 对每条 inbound_id='' 的记录，按 node_id 查找该节点的所有 inbound，各建一条新记录
	rows, err := db.conn.Query(`SELECT id, user_id, node_id, uuid, secret, synced_upload_bytes, synced_download_bytes, created_at FROM user_inbounds WHERE inbound_id = ''`)
	if err != nil {
		return fmt.Errorf("migrate user_inbounds expand: query: %w", err)
	}
	type oldRec struct {
		id, userID, nodeID, uuid, secret string
		up, down                         int64
		createdAt                        string
	}
	var olds []oldRec
	for rows.Next() {
		var r oldRec
		if err := rows.Scan(&r.id, &r.userID, &r.nodeID, &r.uuid, &r.secret, &r.up, &r.down, &r.createdAt); err != nil {
			rows.Close()
			return fmt.Errorf("migrate user_inbounds expand: scan: %w", err)
		}
		olds = append(olds, r)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return fmt.Errorf("migrate user_inbounds expand: rows: %w", err)
	}

	for _, rec := range olds {
		// 查找该节点上的所有 inbound
		ibRows, err := db.conn.Query(`SELECT id FROM inbounds WHERE node_id = ? ORDER BY id`, rec.nodeID)
		if err != nil {
			continue
		}
		var ibIDs []string
		for ibRows.Next() {
			var ibID string
			if ibRows.Scan(&ibID) == nil {
				ibIDs = append(ibIDs, ibID)
			}
		}
		ibRows.Close()

		if len(ibIDs) == 0 {
			// 节点无 inbound，保留原记录不处理（inbound_id 仍为空）
			continue
		}

		// 为第一个 inbound 直接更新原记录（复用游标值）
		if _, err := db.conn.Exec(`UPDATE user_inbounds SET inbound_id = ? WHERE id = ?`, ibIDs[0], rec.id); err != nil {
			continue
		}

		// 为其余 inbound 插入新记录（游标清零，凭据与原记录相同）
		for _, ibID := range ibIDs[1:] {
			newID := rec.userID + "-" + ibID // 简单唯一性保证
			db.conn.Exec(`
				INSERT OR IGNORE INTO user_inbounds (id, user_id, inbound_id, node_id, uuid, secret, synced_upload_bytes, synced_download_bytes, created_at)
				VALUES (?, ?, ?, ?, ?, ?, 0, 0, ?)
			`, newID, rec.userID, ibID, rec.nodeID, rec.uuid, rec.secret, rec.createdAt)
		}
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
			raw_upload_bytes INTEGER NOT NULL DEFAULT 0,
			raw_download_bytes INTEGER NOT NULL DEFAULT 0,
			on_hold_expire_at TEXT,
			last_traffic_reset_at TEXT,
			online_at TEXT,
			connections INTEGER NOT NULL DEFAULT 0,
			devices INTEGER NOT NULL DEFAULT 0,
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
