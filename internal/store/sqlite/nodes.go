package sqlite

import (
	"database/sql"
	"errors"
	"fmt"
	"time"

	"pulse/internal/nodes"
)

type NodeStore struct {
	db *sql.DB
}

func (s *NodeStore) Upsert(node nodes.Node) (nodes.Node, error) {
	if node.TrafficRate <= 0 {
		node.TrafficRate = 1.0
	}
	_, err := s.db.Exec(`
		INSERT INTO nodes (id, name, base_url, certificate, traffic_rate)
		VALUES (?, ?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET
			name = excluded.name,
			base_url = excluded.base_url,
			certificate = excluded.certificate,
			traffic_rate = excluded.traffic_rate
	`, node.ID, node.Name, node.BaseURL, "", node.TrafficRate)
	if err != nil {
		return nodes.Node{}, fmt.Errorf("upsert node: %w", err)
	}
	return node, nil
}

func (s *NodeStore) Delete(id string) error {
	result, err := s.db.Exec(`DELETE FROM nodes WHERE id = ?`, id)
	if err != nil {
		return fmt.Errorf("delete node: %w", err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("delete node rows affected: %w", err)
	}
	if affected == 0 {
		return nodes.ErrNodeNotFound
	}
	return nil
}

func (s *NodeStore) Get(id string) (nodes.Node, error) {
	var node nodes.Node
	var caddyEnabled int
	err := s.db.QueryRow(
		`SELECT id, name, base_url, upload_bytes, download_bytes, caddy_acme_email, caddy_panel_domain, caddy_enabled, traffic_rate
		 FROM nodes WHERE id = ?`, id,
	).Scan(&node.ID, &node.Name, &node.BaseURL, &node.UploadBytes, &node.DownloadBytes, &node.CaddyACMEEmail, &node.CaddyPanelDomain, &caddyEnabled, &node.TrafficRate)
	if errors.Is(err, sql.ErrNoRows) {
		return nodes.Node{}, nodes.ErrNodeNotFound
	}
	if err != nil {
		return nodes.Node{}, fmt.Errorf("get node: %w", err)
	}
	node.CaddyEnabled = caddyEnabled != 0
	if node.TrafficRate <= 0 {
		node.TrafficRate = 1.0
	}
	return node, nil
}

func (s *NodeStore) List() ([]nodes.Node, error) {
	rows, err := s.db.Query(
		`SELECT id, name, base_url, upload_bytes, download_bytes, caddy_acme_email, caddy_panel_domain, caddy_enabled, traffic_rate
		 FROM nodes ORDER BY id`,
	)
	if err != nil {
		return nil, fmt.Errorf("list nodes: %w", err)
	}
	defer rows.Close()

	items := make([]nodes.Node, 0)
	for rows.Next() {
		var node nodes.Node
		var caddyEnabled int
		if err := rows.Scan(&node.ID, &node.Name, &node.BaseURL, &node.UploadBytes, &node.DownloadBytes, &node.CaddyACMEEmail, &node.CaddyPanelDomain, &caddyEnabled, &node.TrafficRate); err != nil {
			return nil, fmt.Errorf("scan node: %w", err)
		}
		node.CaddyEnabled = caddyEnabled != 0
		if node.TrafficRate <= 0 {
			node.TrafficRate = 1.0
		}
		items = append(items, node)
	}
	return items, rows.Err()
}

func (s *NodeStore) UpdateCaddyConfig(nodeID, acmeEmail, panelDomain string, caddyEnabled bool) error {
	enabled := 0
	if caddyEnabled {
		enabled = 1
	}
	_, err := s.db.Exec(
		`UPDATE nodes SET caddy_acme_email = ?, caddy_panel_domain = ?, caddy_enabled = ? WHERE id = ?`,
		acmeEmail, panelDomain, enabled, nodeID,
	)
	if err != nil {
		return fmt.Errorf("update node caddy config: %w", err)
	}
	return nil
}

func (s *NodeStore) AddTraffic(nodeID string, upload, download int64) error {
	_, err := s.db.Exec(
		`UPDATE nodes SET upload_bytes = upload_bytes + ?, download_bytes = download_bytes + ? WHERE id = ?`,
		upload, download, nodeID,
	)
	if err != nil {
		return fmt.Errorf("add node traffic: %w", err)
	}
	return nil
}

func (s *NodeStore) AddNodeDailyUsage(nodeID, date string, upload, download int64) error {
	_, err := s.db.Exec(`
		INSERT INTO node_daily_usage (node_id, date, upload_bytes, download_bytes)
		VALUES (?, ?, ?, ?)
		ON CONFLICT(node_id, date) DO UPDATE SET
			upload_bytes   = upload_bytes   + excluded.upload_bytes,
			download_bytes = download_bytes + excluded.download_bytes
	`, nodeID, date, upload, download)
	if err != nil {
		return fmt.Errorf("add node daily usage: %w", err)
	}
	return nil
}

func (s *NodeStore) ListNodeDailyUsage(days int) ([]nodes.NodeDailyUsage, error) {
	cutoff := time.Now().UTC().AddDate(0, 0, -days).Format("2006-01-02")
	rows, err := s.db.Query(`
		SELECT node_id, date, upload_bytes, download_bytes
		FROM node_daily_usage
		WHERE date >= ?
		ORDER BY date ASC
	`, cutoff)
	if err != nil {
		return nil, fmt.Errorf("list node daily usage: %w", err)
	}
	defer rows.Close()

	var result []nodes.NodeDailyUsage
	for rows.Next() {
		var u nodes.NodeDailyUsage
		if err := rows.Scan(&u.NodeID, &u.Date, &u.UploadBytes, &u.DownloadBytes); err != nil {
			return nil, fmt.Errorf("scan node daily usage: %w", err)
		}
		result = append(result, u)
	}
	return result, rows.Err()
}
