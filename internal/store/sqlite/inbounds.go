package sqlite

import (
	"database/sql"
	"errors"

	"pulse/internal/inbounds"
)

// InboundStore 实现 inbounds.InboundStore 接口。
type InboundStore struct {
	db *sql.DB
}

func (db *DB) InboundStore() *InboundStore {
	return &InboundStore{db: db.conn}
}

// ─── Inbound ──────────────────────────────────────────────────────────────────

func (s *InboundStore) UpsertInbound(inbound inbounds.Inbound) (inbounds.Inbound, error) {
	_, err := s.db.Exec(`
		INSERT INTO inbounds (
			id, node_id, protocol, tag, port,
			method, password, security, reality_private_key, reality_public_key,
			reality_handshake_addr, reality_short_id, outbound_id
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET
			node_id                = excluded.node_id,
			protocol               = excluded.protocol,
			tag                    = excluded.tag,
			port                   = excluded.port,
			method                 = excluded.method,
			password               = excluded.password,
			security               = excluded.security,
			reality_private_key    = excluded.reality_private_key,
			reality_public_key     = excluded.reality_public_key,
			reality_handshake_addr = excluded.reality_handshake_addr,
			reality_short_id       = excluded.reality_short_id,
			outbound_id            = excluded.outbound_id
	`,
		inbound.ID, inbound.NodeID, inbound.Protocol, inbound.Tag, inbound.Port,
		inbound.Method, inbound.Password, inbound.Security, inbound.RealityPrivateKey, inbound.RealityPublicKey,
		inbound.RealityHandshakeAddr, inbound.RealityShortID, inbound.OutboundID,
	)
	if err != nil {
		return inbounds.Inbound{}, err
	}
	return inbound, nil
}

func (s *InboundStore) GetInbound(id string) (inbounds.Inbound, error) {
	row := s.db.QueryRow(`
		SELECT id, node_id, protocol, tag, port,
		       method, password, security, reality_private_key, reality_public_key,
		       reality_handshake_addr, reality_short_id, outbound_id
		FROM inbounds WHERE id = ?
	`, id)
	return scanInbound(row)
}

func (s *InboundStore) ListInbounds() ([]inbounds.Inbound, error) {
	rows, err := s.db.Query(`
		SELECT id, node_id, protocol, tag, port,
		       method, password, security, reality_private_key, reality_public_key,
		       reality_handshake_addr, reality_short_id, outbound_id
		FROM inbounds ORDER BY id
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return collectInbounds(rows)
}

func (s *InboundStore) ListInboundsByNode(nodeID string) ([]inbounds.Inbound, error) {
	rows, err := s.db.Query(`
		SELECT id, node_id, protocol, tag, port,
		       method, password, security, reality_private_key, reality_public_key,
		       reality_handshake_addr, reality_short_id, outbound_id
		FROM inbounds WHERE node_id = ? ORDER BY id
	`, nodeID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return collectInbounds(rows)
}

func (s *InboundStore) DeleteInbound(id string) error {
	res, err := s.db.Exec(`DELETE FROM inbounds WHERE id = ?`, id)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return inbounds.ErrInboundNotFound
	}
	return nil
}

func scanInbound(row *sql.Row) (inbounds.Inbound, error) {
	var in inbounds.Inbound
	err := row.Scan(
		&in.ID, &in.NodeID, &in.Protocol, &in.Tag, &in.Port,
		&in.Method, &in.Password, &in.Security, &in.RealityPrivateKey, &in.RealityPublicKey,
		&in.RealityHandshakeAddr, &in.RealityShortID, &in.OutboundID,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return inbounds.Inbound{}, inbounds.ErrInboundNotFound
	}
	return in, err
}

func collectInbounds(rows *sql.Rows) ([]inbounds.Inbound, error) {
	out := make([]inbounds.Inbound, 0)
	for rows.Next() {
		var in inbounds.Inbound
		if err := rows.Scan(
			&in.ID, &in.NodeID, &in.Protocol, &in.Tag, &in.Port,
			&in.Method, &in.Password, &in.Security, &in.RealityPrivateKey, &in.RealityPublicKey,
			&in.RealityHandshakeAddr, &in.RealityShortID, &in.OutboundID,
		); err != nil {
			return nil, err
		}
		out = append(out, in)
	}
	return out, rows.Err()
}

// ─── Host ─────────────────────────────────────────────────────────────────────

func (s *InboundStore) UpsertHost(host inbounds.Host) (inbounds.Host, error) {
	_, err := s.db.Exec(`
		INSERT INTO hosts (
			id, inbound_id, remark, address, port, sni, host, path,
			security, alpn, fingerprint, allow_insecure, mux_enable,
			reality_public_key, reality_short_id, reality_spider_x
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET
			inbound_id         = excluded.inbound_id,
			remark             = excluded.remark,
			address            = excluded.address,
			port               = excluded.port,
			sni                = excluded.sni,
			host               = excluded.host,
			path               = excluded.path,
			security           = excluded.security,
			alpn               = excluded.alpn,
			fingerprint        = excluded.fingerprint,
			allow_insecure     = excluded.allow_insecure,
			mux_enable         = excluded.mux_enable,
			reality_public_key = excluded.reality_public_key,
			reality_short_id   = excluded.reality_short_id,
			reality_spider_x   = excluded.reality_spider_x
	`,
		host.ID, host.InboundID, host.Remark, host.Address, host.Port,
		host.SNI, host.Host, host.Path, host.Security, host.ALPN, host.Fingerprint,
		boolToInt(host.AllowInsecure), boolToInt(host.MuxEnable),
		host.RealityPublicKey, host.RealityShortID, host.RealitySpiderX,
	)
	if err != nil {
		return inbounds.Host{}, err
	}
	return host, nil
}

func (s *InboundStore) GetHost(id string) (inbounds.Host, error) {
	row := s.db.QueryRow(`
		SELECT id, inbound_id, remark, address, port, sni, host, path,
		       security, alpn, fingerprint, allow_insecure, mux_enable,
		       reality_public_key, reality_short_id, reality_spider_x
		FROM hosts WHERE id = ?
	`, id)
	return scanHost(row)
}

func (s *InboundStore) ListHosts() ([]inbounds.Host, error) {
	rows, err := s.db.Query(`
		SELECT id, inbound_id, remark, address, port, sni, host, path,
		       security, alpn, fingerprint, allow_insecure, mux_enable,
		       reality_public_key, reality_short_id, reality_spider_x
		FROM hosts ORDER BY id
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return collectHosts(rows)
}

func (s *InboundStore) ListHostsByInbound(inboundID string) ([]inbounds.Host, error) {
	rows, err := s.db.Query(`
		SELECT id, inbound_id, remark, address, port, sni, host, path,
		       security, alpn, fingerprint, allow_insecure, mux_enable,
		       reality_public_key, reality_short_id, reality_spider_x
		FROM hosts WHERE inbound_id = ? ORDER BY id
	`, inboundID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return collectHosts(rows)
}

func (s *InboundStore) DeleteHost(id string) error {
	res, err := s.db.Exec(`DELETE FROM hosts WHERE id = ?`, id)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return inbounds.ErrHostNotFound
	}
	return nil
}

func scanHost(row *sql.Row) (inbounds.Host, error) {
	var h inbounds.Host
	var allowInsecure, muxEnable int
	err := row.Scan(
		&h.ID, &h.InboundID, &h.Remark, &h.Address, &h.Port,
		&h.SNI, &h.Host, &h.Path, &h.Security, &h.ALPN, &h.Fingerprint,
		&allowInsecure, &muxEnable,
		&h.RealityPublicKey, &h.RealityShortID, &h.RealitySpiderX,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return inbounds.Host{}, inbounds.ErrHostNotFound
	}
	h.AllowInsecure = allowInsecure != 0
	h.MuxEnable = muxEnable != 0
	return h, err
}

func collectHosts(rows *sql.Rows) ([]inbounds.Host, error) {
	out := make([]inbounds.Host, 0)
	for rows.Next() {
		var h inbounds.Host
		var allowInsecure, muxEnable int
		if err := rows.Scan(
			&h.ID, &h.InboundID, &h.Remark, &h.Address, &h.Port,
			&h.SNI, &h.Host, &h.Path, &h.Security, &h.ALPN, &h.Fingerprint,
			&allowInsecure, &muxEnable,
			&h.RealityPublicKey, &h.RealityShortID, &h.RealitySpiderX,
		); err != nil {
			return nil, err
		}
		h.AllowInsecure = allowInsecure != 0
		h.MuxEnable = muxEnable != 0
		out = append(out, h)
	}
	return out, rows.Err()
}

func boolToInt(b bool) int {
	if b {
		return 1
	}
	return 0
}
