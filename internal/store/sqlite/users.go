package sqlite

import (
	"database/sql"
	"errors"
	"fmt"
	"time"

	"pulse/internal/users"
)

type UserStore struct {
	db *sql.DB
}

func (s *UserStore) Upsert(user users.User) (users.User, error) {
	if user.CreatedAt.IsZero() {
		user.CreatedAt = time.Now().UTC()
	}
	if user.Protocol == "" {
		user.Protocol = "vless"
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
			id, username, uuid, protocol, secret, method,
			status, expire_at, data_limit_reset_strategy,
			node_id, domain, port, inbound_tag,
			traffic_limit_bytes, upload_bytes, download_bytes, used_bytes,
			synced_upload_bytes, synced_download_bytes,
			apply_count, last_applied_at, last_traffic_reset_at, created_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET
			username = excluded.username,
			uuid = excluded.uuid,
			protocol = excluded.protocol,
			secret = excluded.secret,
			method = excluded.method,
			status = excluded.status,
			expire_at = excluded.expire_at,
			data_limit_reset_strategy = excluded.data_limit_reset_strategy,
			node_id = excluded.node_id,
			domain = excluded.domain,
			port = excluded.port,
			inbound_tag = excluded.inbound_tag,
			traffic_limit_bytes = excluded.traffic_limit_bytes,
			upload_bytes = excluded.upload_bytes,
			download_bytes = excluded.download_bytes,
			used_bytes = excluded.used_bytes,
			synced_upload_bytes = excluded.synced_upload_bytes,
			synced_download_bytes = excluded.synced_download_bytes,
			apply_count = excluded.apply_count,
			last_applied_at = excluded.last_applied_at,
			last_traffic_reset_at = excluded.last_traffic_reset_at,
			created_at = excluded.created_at
	`,
		user.ID, user.Username, user.UUID, user.Protocol, user.Secret, user.Method,
		user.Status, formatTimePtr(user.ExpireAt), user.DataLimitResetStrategy,
		user.NodeID, user.Domain, user.Port, user.InboundTag,
		user.TrafficLimit, user.UploadBytes, user.DownloadBytes, user.UsedBytes,
		user.SyncedUploadBytes, user.SyncedDownloadBytes,
		user.ApplyCount, formatTime(user.LastAppliedAt), formatTimePtr(user.LastTrafficResetAt), user.CreatedAt.Format(time.RFC3339Nano),
	)
	if err != nil {
		return users.User{}, fmt.Errorf("upsert user: %w", err)
	}
	return user, nil
}

func (s *UserStore) Get(id string) (users.User, error) {
	row := s.db.QueryRow(`
		SELECT id, username, uuid, protocol, secret, method,
		       status, expire_at, data_limit_reset_strategy,
		       node_id, domain, port, inbound_tag,
		       traffic_limit_bytes, upload_bytes, download_bytes, used_bytes,
		       synced_upload_bytes, synced_download_bytes,
		       apply_count, last_applied_at, last_traffic_reset_at, created_at
		FROM users WHERE id = ?
	`, id)
	return scanUser(row)
}

func (s *UserStore) List() ([]users.User, error) {
	rows, err := s.db.Query(`
		SELECT id, username, uuid, protocol, secret, method,
		       status, expire_at, data_limit_reset_strategy,
		       node_id, domain, port, inbound_tag,
		       traffic_limit_bytes, upload_bytes, download_bytes, used_bytes,
		       synced_upload_bytes, synced_download_bytes,
		       apply_count, last_applied_at, last_traffic_reset_at, created_at
		FROM users ORDER BY id
	`)
	if err != nil {
		return nil, fmt.Errorf("list users: %w", err)
	}
	defer rows.Close()
	return scanUsers(rows)
}

func (s *UserStore) ListByNode(nodeID string) ([]users.User, error) {
	rows, err := s.db.Query(`
		SELECT id, username, uuid, protocol, secret, method,
		       status, expire_at, data_limit_reset_strategy,
		       node_id, domain, port, inbound_tag,
		       traffic_limit_bytes, upload_bytes, download_bytes, used_bytes,
		       synced_upload_bytes, synced_download_bytes,
		       apply_count, last_applied_at, last_traffic_reset_at, created_at
		FROM users WHERE node_id = ? ORDER BY id
	`, nodeID)
	if err != nil {
		return nil, fmt.Errorf("list users by node: %w", err)
	}
	defer rows.Close()
	return scanUsers(rows)
}

func (s *UserStore) Delete(id string) error {
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

type scanner interface {
	Scan(dest ...any) error
}

func scanUser(row scanner) (users.User, error) {
	var user users.User
	var expireAt sql.NullString
	var lastAppliedAt string
	var lastTrafficResetAt sql.NullString
	var createdAt string

	err := row.Scan(
		&user.ID, &user.Username, &user.UUID, &user.Protocol, &user.Secret, &user.Method,
		&user.Status, &expireAt, &user.DataLimitResetStrategy,
		&user.NodeID, &user.Domain, &user.Port, &user.InboundTag,
		&user.TrafficLimit, &user.UploadBytes, &user.DownloadBytes, &user.UsedBytes,
		&user.SyncedUploadBytes, &user.SyncedDownloadBytes,
		&user.ApplyCount, &lastAppliedAt, &lastTrafficResetAt, &createdAt,
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
	if lastAppliedAt != "" {
		t, err := time.Parse(time.RFC3339Nano, lastAppliedAt)
		if err != nil {
			return users.User{}, fmt.Errorf("parse user last_applied_at: %w", err)
		}
		user.LastAppliedAt = t
	}
	return user, nil
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
