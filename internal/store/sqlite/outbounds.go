package sqlite

import (
	"database/sql"
	"errors"
	"fmt"

	"pulse/internal/outbounds"
)

// OutboundStore 实现 outbounds.Store 接口。
type OutboundStore struct {
	db *sql.DB
}

func (db *DB) OutboundStore() *OutboundStore {
	return &OutboundStore{db: db.conn}
}

func (s *OutboundStore) Upsert(ob outbounds.Outbound) (outbounds.Outbound, error) {
	_, err := s.db.Exec(`
		INSERT INTO outbounds (id, name, protocol, server, username, password)
		VALUES (?, ?, ?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET
			name     = excluded.name,
			protocol = excluded.protocol,
			server   = excluded.server,
			username = excluded.username,
			password = excluded.password
	`, ob.ID, ob.Name, ob.Protocol, ob.Server, ob.Username, ob.Password)
	if err != nil {
		return outbounds.Outbound{}, fmt.Errorf("upsert outbound: %w", err)
	}
	return ob, nil
}

func (s *OutboundStore) Get(id string) (outbounds.Outbound, error) {
	var ob outbounds.Outbound
	err := s.db.QueryRow(
		`SELECT id, name, protocol, server, username, password FROM outbounds WHERE id = ?`, id,
	).Scan(&ob.ID, &ob.Name, &ob.Protocol, &ob.Server, &ob.Username, &ob.Password)
	if errors.Is(err, sql.ErrNoRows) {
		return outbounds.Outbound{}, outbounds.ErrOutboundNotFound
	}
	if err != nil {
		return outbounds.Outbound{}, fmt.Errorf("get outbound: %w", err)
	}
	return ob, nil
}

func (s *OutboundStore) List() ([]outbounds.Outbound, error) {
	rows, err := s.db.Query(
		`SELECT id, name, protocol, server, username, password FROM outbounds ORDER BY id`,
	)
	if err != nil {
		return nil, fmt.Errorf("list outbounds: %w", err)
	}
	defer rows.Close()

	items := make([]outbounds.Outbound, 0)
	for rows.Next() {
		var ob outbounds.Outbound
		if err := rows.Scan(&ob.ID, &ob.Name, &ob.Protocol, &ob.Server, &ob.Username, &ob.Password); err != nil {
			return nil, fmt.Errorf("scan outbound: %w", err)
		}
		items = append(items, ob)
	}
	return items, rows.Err()
}

func (s *OutboundStore) Delete(id string) error {
	result, err := s.db.Exec(`DELETE FROM outbounds WHERE id = ?`, id)
	if err != nil {
		return fmt.Errorf("delete outbound: %w", err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("delete outbound rows affected: %w", err)
	}
	if affected == 0 {
		return outbounds.ErrOutboundNotFound
	}
	return nil
}
