package sqlite

import (
	"database/sql"
	"errors"
	"fmt"

	"pulse/internal/routerules"
)

// RouteRuleStore 实现 routerules.Store 接口。
type RouteRuleStore struct {
	db *sql.DB
}

func (db *DB) RouteRuleStore() *RouteRuleStore {
	return &RouteRuleStore{db: db.conn}
}

func (s *RouteRuleStore) Upsert(rule routerules.RouteRule) (routerules.RouteRule, error) {
	_, err := s.db.Exec(`
		INSERT INTO route_rules (id, name, rule_type, patterns, outbound_id, priority)
		VALUES (?, ?, ?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET
			name        = excluded.name,
			rule_type   = excluded.rule_type,
			patterns    = excluded.patterns,
			outbound_id = excluded.outbound_id,
			priority    = excluded.priority
	`, rule.ID, rule.Name, rule.RuleType, rule.Patterns, rule.OutboundID, rule.Priority)
	if err != nil {
		return routerules.RouteRule{}, fmt.Errorf("upsert route rule: %w", err)
	}
	return rule, nil
}

func (s *RouteRuleStore) Get(id string) (routerules.RouteRule, error) {
	var r routerules.RouteRule
	err := s.db.QueryRow(
		`SELECT id, name, rule_type, patterns, outbound_id, priority FROM route_rules WHERE id = ?`, id,
	).Scan(&r.ID, &r.Name, &r.RuleType, &r.Patterns, &r.OutboundID, &r.Priority)
	if errors.Is(err, sql.ErrNoRows) {
		return routerules.RouteRule{}, routerules.ErrNotFound
	}
	if err != nil {
		return routerules.RouteRule{}, fmt.Errorf("get route rule: %w", err)
	}
	return r, nil
}

func (s *RouteRuleStore) List() ([]routerules.RouteRule, error) {
	rows, err := s.db.Query(
		`SELECT id, name, rule_type, patterns, outbound_id, priority FROM route_rules ORDER BY priority, id`,
	)
	if err != nil {
		return nil, fmt.Errorf("list route rules: %w", err)
	}
	defer rows.Close()

	items := make([]routerules.RouteRule, 0)
	for rows.Next() {
		var r routerules.RouteRule
		if err := rows.Scan(&r.ID, &r.Name, &r.RuleType, &r.Patterns, &r.OutboundID, &r.Priority); err != nil {
			return nil, fmt.Errorf("scan route rule: %w", err)
		}
		items = append(items, r)
	}
	return items, rows.Err()
}

func (s *RouteRuleStore) Delete(id string) error {
	result, err := s.db.Exec(`DELETE FROM route_rules WHERE id = ?`, id)
	if err != nil {
		return fmt.Errorf("delete route rule: %w", err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("delete route rule rows affected: %w", err)
	}
	if affected == 0 {
		return routerules.ErrNotFound
	}
	return nil
}
