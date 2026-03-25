package sqlite

import (
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"pulse/internal/users"
)

type UserStore struct {
	db *sql.DB
}

// ─── User CRUD ────────────────────────────────────────────────────────────────

func (s *UserStore) UpsertUser(user users.User) (users.User, error) {
	if user.CreatedAt.IsZero() {
		user.CreatedAt = time.Now().UTC()
	}
	if user.Status == "" {
		user.Status = users.StatusActive
	}
	if user.DataLimitResetStrategy == "" {
		user.DataLimitResetStrategy = users.ResetStrategyNoReset
	}
	user.UsedBytes = user.UploadBytes + user.DownloadBytes

	_, err := s.db.Exec(`
		INSERT INTO users (
			id, username, status, expire_at, data_limit_reset_strategy,
			traffic_limit_bytes, upload_bytes, download_bytes, used_bytes,
			last_traffic_reset_at, created_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET
			username = excluded.username,
			status = excluded.status,
			expire_at = excluded.expire_at,
			data_limit_reset_strategy = excluded.data_limit_reset_strategy,
			traffic_limit_bytes = excluded.traffic_limit_bytes,
			upload_bytes = excluded.upload_bytes,
			download_bytes = excluded.download_bytes,
			used_bytes = excluded.used_bytes,
			last_traffic_reset_at = excluded.last_traffic_reset_at,
			created_at = excluded.created_at
	`,
		user.ID, user.Username, user.Status, formatTimePtr(user.ExpireAt), user.DataLimitResetStrategy,
		user.TrafficLimit, user.UploadBytes, user.DownloadBytes, user.UsedBytes,
		formatTimePtr(user.LastTrafficResetAt), user.CreatedAt.Format(time.RFC3339Nano),
	)
	if err != nil {
		return users.User{}, fmt.Errorf("upsert user: %w", err)
	}
	return user, nil
}

func (s *UserStore) GetUser(id string) (users.User, error) {
	row := s.db.QueryRow(`
		SELECT id, username, status, expire_at, data_limit_reset_strategy,
		       traffic_limit_bytes, upload_bytes, download_bytes, used_bytes,
		       last_traffic_reset_at, created_at
		FROM users WHERE id = ?
	`, id)
	return scanUser(row)
}

func (s *UserStore) ListUsers() ([]users.User, error) {
	rows, err := s.db.Query(`
		SELECT id, username, status, expire_at, data_limit_reset_strategy,
		       traffic_limit_bytes, upload_bytes, download_bytes, used_bytes,
		       last_traffic_reset_at, created_at
		FROM users ORDER BY id
	`)
	if err != nil {
		return nil, fmt.Errorf("list users: %w", err)
	}
	defer rows.Close()
	return scanUsers(rows)
}

func (s *UserStore) DeleteUser(id string) error {
	result, err := s.db.Exec(`DELETE FROM users WHERE id = ?`, id)
	if err != nil {
		return fmt.Errorf("delete user: %w", err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("delete user rows affected: %w", err)
	}
	if affected == 0 {
		return users.ErrUserNotFound
	}
	return nil
}

func (s *UserStore) GetUsersByIDs(ids []string) (map[string]users.User, error) {
	if len(ids) == 0 {
		return map[string]users.User{}, nil
	}
	placeholders := make([]string, len(ids))
	args := make([]any, len(ids))
	for i, id := range ids {
		placeholders[i] = "?"
		args[i] = id
	}
	query := fmt.Sprintf(`
		SELECT id, username, status, expire_at, data_limit_reset_strategy,
		       traffic_limit_bytes, upload_bytes, download_bytes, used_bytes,
		       last_traffic_reset_at, created_at
		FROM users WHERE id IN (%s)
	`, strings.Join(placeholders, ","))

	rows, err := s.db.Query(query, args...)
	if err != nil {
		return nil, fmt.Errorf("get users by ids: %w", err)
	}
	defer rows.Close()

	out := make(map[string]users.User)
	for rows.Next() {
		user, err := scanUser(rows)
		if err != nil {
			return nil, err
		}
		out[user.ID] = user
	}
	return out, rows.Err()
}

// ─── UserInbound CRUD ─────────────────────────────────────────────────────────

func (s *UserStore) UpsertUserInbound(ib users.UserInbound) (users.UserInbound, error) {
	if ib.CreatedAt.IsZero() {
		ib.CreatedAt = time.Now().UTC()
	}
	if ib.Protocol == "" {
		ib.Protocol = "vless"
	}

	_, err := s.db.Exec(`
		INSERT INTO user_inbounds (
			id, user_id, node_id, protocol, uuid, secret, method,
			security, flow, sni, fingerprint,
			reality_public_key, reality_short_id, reality_spider_x,
			reality_private_key, reality_handshake_addr,
			domain, port, inbound_tag,
			synced_upload_bytes, synced_download_bytes,
			apply_count, last_applied_at, created_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET
			user_id = excluded.user_id,
			node_id = excluded.node_id,
			protocol = excluded.protocol,
			uuid = excluded.uuid,
			secret = excluded.secret,
			method = excluded.method,
			security = excluded.security,
			flow = excluded.flow,
			sni = excluded.sni,
			fingerprint = excluded.fingerprint,
			reality_public_key = excluded.reality_public_key,
			reality_short_id = excluded.reality_short_id,
			reality_spider_x = excluded.reality_spider_x,
			reality_private_key = excluded.reality_private_key,
			reality_handshake_addr = excluded.reality_handshake_addr,
			domain = excluded.domain,
			port = excluded.port,
			inbound_tag = excluded.inbound_tag,
			synced_upload_bytes = excluded.synced_upload_bytes,
			synced_download_bytes = excluded.synced_download_bytes,
			apply_count = excluded.apply_count,
			last_applied_at = excluded.last_applied_at,
			created_at = excluded.created_at
	`,
		ib.ID, ib.UserID, ib.NodeID, ib.Protocol, ib.UUID, ib.Secret, ib.Method,
		ib.Security, ib.Flow, ib.SNI, ib.Fingerprint,
		ib.RealityPublicKey, ib.RealityShortID, ib.RealitySpiderX,
		ib.RealityPrivateKey, ib.RealityHandshakeAddr,
		ib.Domain, ib.Port, ib.InboundTag,
		ib.SyncedUploadBytes, ib.SyncedDownloadBytes,
		ib.ApplyCount, formatTime(ib.LastAppliedAt), ib.CreatedAt.Format(time.RFC3339Nano),
	)
	if err != nil {
		return users.UserInbound{}, fmt.Errorf("upsert user inbound: %w", err)
	}
	return ib, nil
}

func (s *UserStore) GetUserInbound(id string) (users.UserInbound, error) {
	row := s.db.QueryRow(`
		SELECT id, user_id, node_id, protocol, uuid, secret, method,
		       security, flow, sni, fingerprint,
		       reality_public_key, reality_short_id, reality_spider_x,
		       reality_private_key, reality_handshake_addr,
		       domain, port, inbound_tag,
		       synced_upload_bytes, synced_download_bytes,
		       apply_count, last_applied_at, created_at
		FROM user_inbounds WHERE id = ?
	`, id)
	return scanUserInbound(row)
}

func (s *UserStore) ListUserInbounds() ([]users.UserInbound, error) {
	rows, err := s.db.Query(`
		SELECT id, user_id, node_id, protocol, uuid, secret, method,
		       security, flow, sni, fingerprint,
		       reality_public_key, reality_short_id, reality_spider_x,
		       reality_private_key, reality_handshake_addr,
		       domain, port, inbound_tag,
		       synced_upload_bytes, synced_download_bytes,
		       apply_count, last_applied_at, created_at
		FROM user_inbounds ORDER BY id
	`)
	if err != nil {
		return nil, fmt.Errorf("list user inbounds: %w", err)
	}
	defer rows.Close()
	return scanUserInbounds(rows)
}

func (s *UserStore) ListUserInboundsByUser(userID string) ([]users.UserInbound, error) {
	rows, err := s.db.Query(`
		SELECT id, user_id, node_id, protocol, uuid, secret, method,
		       security, flow, sni, fingerprint,
		       reality_public_key, reality_short_id, reality_spider_x,
		       reality_private_key, reality_handshake_addr,
		       domain, port, inbound_tag,
		       synced_upload_bytes, synced_download_bytes,
		       apply_count, last_applied_at, created_at
		FROM user_inbounds WHERE user_id = ? ORDER BY id
	`, userID)
	if err != nil {
		return nil, fmt.Errorf("list user inbounds by user: %w", err)
	}
	defer rows.Close()
	return scanUserInbounds(rows)
}

func (s *UserStore) ListUserInboundsByNode(nodeID string) ([]users.UserInbound, error) {
	rows, err := s.db.Query(`
		SELECT id, user_id, node_id, protocol, uuid, secret, method,
		       security, flow, sni, fingerprint,
		       reality_public_key, reality_short_id, reality_spider_x,
		       reality_private_key, reality_handshake_addr,
		       domain, port, inbound_tag,
		       synced_upload_bytes, synced_download_bytes,
		       apply_count, last_applied_at, created_at
		FROM user_inbounds WHERE node_id = ? ORDER BY id
	`, nodeID)
	if err != nil {
		return nil, fmt.Errorf("list user inbounds by node: %w", err)
	}
	defer rows.Close()
	return scanUserInbounds(rows)
}

// ListCursorInboundsByNode 返回每个 (node_id, user_id) 组合中 id 最小的 inbound。
func (s *UserStore) ListCursorInboundsByNode(nodeID string) ([]users.UserInbound, error) {
	rows, err := s.db.Query(`
		SELECT ui.id, ui.user_id, ui.node_id, ui.protocol, ui.uuid, ui.secret, ui.method,
		       ui.security, ui.flow, ui.sni, ui.fingerprint,
		       ui.reality_public_key, ui.reality_short_id, ui.reality_spider_x,
		       ui.reality_private_key, ui.reality_handshake_addr,
		       ui.domain, ui.port, ui.inbound_tag,
		       ui.synced_upload_bytes, ui.synced_download_bytes,
		       ui.apply_count, ui.last_applied_at, ui.created_at
		FROM user_inbounds ui
		WHERE ui.node_id = ?
		  AND ui.id = (
		    SELECT MIN(id) FROM user_inbounds
		    WHERE node_id = ui.node_id AND user_id = ui.user_id
		  )
		ORDER BY ui.user_id
	`, nodeID)
	if err != nil {
		return nil, fmt.Errorf("list cursor inbounds by node: %w", err)
	}
	defer rows.Close()
	return scanUserInbounds(rows)
}

func (s *UserStore) DeleteUserInbound(id string) error {
	result, err := s.db.Exec(`DELETE FROM user_inbounds WHERE id = ?`, id)
	if err != nil {
		return fmt.Errorf("delete user inbound: %w", err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("delete user inbound rows affected: %w", err)
	}
	if affected == 0 {
		return users.ErrUserInboundNotFound
	}
	return nil
}

func (s *UserStore) DeleteUserInboundsByUser(userID string) error {
	if _, err := s.db.Exec(`DELETE FROM user_inbounds WHERE user_id = ?`, userID); err != nil {
		return fmt.Errorf("delete user inbounds by user: %w", err)
	}
	return nil
}

// ─── 扫描辅助 ─────────────────────────────────────────────────────────────────

type scanner interface {
	Scan(dest ...any) error
}

func scanUsers(rows *sql.Rows) ([]users.User, error) {
	items := make([]users.User, 0)
	for rows.Next() {
		user, err := scanUser(rows)
		if err != nil {
			return nil, err
		}
		items = append(items, user)
	}
	return items, rows.Err()
}

func scanUser(row scanner) (users.User, error) {
	var user users.User
	var expireAt sql.NullString
	var lastTrafficResetAt sql.NullString
	var createdAt string

	err := row.Scan(
		&user.ID, &user.Username, &user.Status, &expireAt, &user.DataLimitResetStrategy,
		&user.TrafficLimit, &user.UploadBytes, &user.DownloadBytes, &user.UsedBytes,
		&lastTrafficResetAt, &createdAt,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return users.User{}, users.ErrUserNotFound
	}
	if err != nil {
		return users.User{}, fmt.Errorf("scan user: %w", err)
	}

	if user.UsedBytes == 0 {
		user.UsedBytes = user.UploadBytes + user.DownloadBytes
	}
	if user.Status == "" {
		user.Status = users.StatusActive
	}
	if user.DataLimitResetStrategy == "" {
		user.DataLimitResetStrategy = users.ResetStrategyNoReset
	}

	if expireAt.Valid && expireAt.String != "" {
		t, err := time.Parse(time.RFC3339Nano, expireAt.String)
		if err != nil {
			return users.User{}, fmt.Errorf("parse user expire_at: %w", err)
		}
		user.ExpireAt = &t
	}
	if lastTrafficResetAt.Valid && lastTrafficResetAt.String != "" {
		t, err := time.Parse(time.RFC3339Nano, lastTrafficResetAt.String)
		if err != nil {
			return users.User{}, fmt.Errorf("parse user last_traffic_reset_at: %w", err)
		}
		user.LastTrafficResetAt = &t
	}
	if createdAt != "" {
		t, err := time.Parse(time.RFC3339Nano, createdAt)
		if err != nil {
			return users.User{}, fmt.Errorf("parse user created_at: %w", err)
		}
		user.CreatedAt = t
	}
	return user, nil
}

func scanUserInbounds(rows *sql.Rows) ([]users.UserInbound, error) {
	items := make([]users.UserInbound, 0)
	for rows.Next() {
		ib, err := scanUserInbound(rows)
		if err != nil {
			return nil, err
		}
		items = append(items, ib)
	}
	return items, rows.Err()
}

func scanUserInbound(row scanner) (users.UserInbound, error) {
	var ib users.UserInbound
	var lastAppliedAt string
	var createdAt string

	err := row.Scan(
		&ib.ID, &ib.UserID, &ib.NodeID, &ib.Protocol, &ib.UUID, &ib.Secret, &ib.Method,
		&ib.Security, &ib.Flow, &ib.SNI, &ib.Fingerprint,
		&ib.RealityPublicKey, &ib.RealityShortID, &ib.RealitySpiderX,
		&ib.RealityPrivateKey, &ib.RealityHandshakeAddr,
		&ib.Domain, &ib.Port, &ib.InboundTag,
		&ib.SyncedUploadBytes, &ib.SyncedDownloadBytes,
		&ib.ApplyCount, &lastAppliedAt, &createdAt,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return users.UserInbound{}, users.ErrUserInboundNotFound
	}
	if err != nil {
		return users.UserInbound{}, fmt.Errorf("scan user inbound: %w", err)
	}

	if createdAt != "" {
		t, err := time.Parse(time.RFC3339Nano, createdAt)
		if err != nil {
			return users.UserInbound{}, fmt.Errorf("parse inbound created_at: %w", err)
		}
		ib.CreatedAt = t
	}
	if lastAppliedAt != "" {
		t, err := time.Parse(time.RFC3339Nano, lastAppliedAt)
		if err != nil {
			return users.UserInbound{}, fmt.Errorf("parse inbound last_applied_at: %w", err)
		}
		ib.LastAppliedAt = t
	}
	return ib, nil
}

func formatTime(value time.Time) string {
	if value.IsZero() {
		return ""
	}
	return value.Format(time.RFC3339Nano)
}

func formatTimePtr(value *time.Time) sql.NullString {
	if value == nil || value.IsZero() {
		return sql.NullString{}
	}
	return sql.NullString{String: value.Format(time.RFC3339Nano), Valid: true}
}
