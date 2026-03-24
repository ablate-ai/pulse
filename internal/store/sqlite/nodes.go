package sqlite

import (
	"database/sql"
	"errors"
	"fmt"

	"pulse/internal/nodes"
)

type NodeStore struct {
	db *sql.DB
}

func (s *NodeStore) Upsert(node nodes.Node) (nodes.Node, error) {
	_, err := s.db.Exec(`
		INSERT INTO nodes (id, name, base_url, auth_token)
		VALUES (?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET
			name = excluded.name,
			base_url = excluded.base_url,
			auth_token = excluded.auth_token
	`, node.ID, node.Name, node.BaseURL, node.AuthToken)
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
	err := s.db.QueryRow(`SELECT id, name, base_url, auth_token FROM nodes WHERE id = ?`, id).
		Scan(&node.ID, &node.Name, &node.BaseURL, &node.AuthToken)
	if errors.Is(err, sql.ErrNoRows) {
		return nodes.Node{}, nodes.ErrNodeNotFound
	}
	if err != nil {
		return nodes.Node{}, fmt.Errorf("get node: %w", err)
	}
	return node, nil
}

func (s *NodeStore) List() ([]nodes.Node, error) {
	rows, err := s.db.Query(`SELECT id, name, base_url, auth_token FROM nodes ORDER BY id`)
	if err != nil {
		return nil, fmt.Errorf("list nodes: %w", err)
	}
	defer rows.Close()

	items := make([]nodes.Node, 0)
	for rows.Next() {
		var node nodes.Node
		if err := rows.Scan(&node.ID, &node.Name, &node.BaseURL, &node.AuthToken); err != nil {
			return nil, fmt.Errorf("scan node: %w", err)
		}
		items = append(items, node)
	}
	return items, rows.Err()
}
