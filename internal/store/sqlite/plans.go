package sqlite

import (
	"database/sql"
	"errors"
	"fmt"
	"time"

	"pulse/internal/plans"
)

type PlanStore struct {
	db *sql.DB
}

func (s *PlanStore) UpsertPlan(plan plans.Plan) (plans.Plan, error) {
	if plan.CreatedAt.IsZero() {
		plan.CreatedAt = time.Now().UTC()
	}
	if plan.Currency == "" {
		plan.Currency = "usd"
	}

	_, err := s.db.Exec(`
		INSERT INTO plans (
			id, name, description, type, price_cents, currency,
			stripe_price_id, traffic_limit, duration_days,
			data_limit_reset_strategy, inbound_ids, sort_order, enabled, created_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET
			name = excluded.name,
			description = excluded.description,
			type = excluded.type,
			price_cents = excluded.price_cents,
			currency = excluded.currency,
			stripe_price_id = excluded.stripe_price_id,
			traffic_limit = excluded.traffic_limit,
			duration_days = excluded.duration_days,
			data_limit_reset_strategy = excluded.data_limit_reset_strategy,
			inbound_ids = excluded.inbound_ids,
			sort_order = excluded.sort_order,
			enabled = excluded.enabled,
			created_at = excluded.created_at
	`,
		plan.ID, plan.Name, plan.Description, plan.Type, plan.PriceCents, plan.Currency,
		plan.StripePriceID, plan.TrafficLimit, plan.DurationDays,
		plan.DataLimitResetStrategy, plan.InboundIDs, plan.SortOrder, boolToInt(plan.Enabled),
		plan.CreatedAt.Format(time.RFC3339Nano),
	)
	if err != nil {
		return plans.Plan{}, fmt.Errorf("upsert plan: %w", err)
	}
	return plan, nil
}

func (s *PlanStore) GetPlan(id string) (plans.Plan, error) {
	row := s.db.QueryRow(`
		SELECT id, name, description, type, price_cents, currency,
		       stripe_price_id, traffic_limit, duration_days,
		       data_limit_reset_strategy, inbound_ids, sort_order, enabled, created_at
		FROM plans WHERE id = ?
	`, id)
	return scanPlan(row)
}

func (s *PlanStore) ListPlans() ([]plans.Plan, error) {
	rows, err := s.db.Query(`
		SELECT id, name, description, type, price_cents, currency,
		       stripe_price_id, traffic_limit, duration_days,
		       data_limit_reset_strategy, inbound_ids, sort_order, enabled, created_at
		FROM plans ORDER BY sort_order, id
	`)
	if err != nil {
		return nil, fmt.Errorf("list plans: %w", err)
	}
	defer rows.Close()
	return scanPlans(rows)
}

func (s *PlanStore) ListEnabledPlans() ([]plans.Plan, error) {
	rows, err := s.db.Query(`
		SELECT id, name, description, type, price_cents, currency,
		       stripe_price_id, traffic_limit, duration_days,
		       data_limit_reset_strategy, inbound_ids, sort_order, enabled, created_at
		FROM plans WHERE enabled = 1 ORDER BY sort_order, id
	`)
	if err != nil {
		return nil, fmt.Errorf("list enabled plans: %w", err)
	}
	defer rows.Close()
	return scanPlans(rows)
}

func (s *PlanStore) DeletePlan(id string) error {
	result, err := s.db.Exec(`DELETE FROM plans WHERE id = ?`, id)
	if err != nil {
		return fmt.Errorf("delete plan: %w", err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("delete plan rows affected: %w", err)
	}
	if affected == 0 {
		return plans.ErrPlanNotFound
	}
	return nil
}

// ─── 扫描辅助 ─────────────────────────────────────────────────────────────────

func scanPlans(rows *sql.Rows) ([]plans.Plan, error) {
	items := make([]plans.Plan, 0)
	for rows.Next() {
		p, err := scanPlan(rows)
		if err != nil {
			return nil, err
		}
		items = append(items, p)
	}
	return items, rows.Err()
}

func scanPlan(row scanner) (plans.Plan, error) {
	var p plans.Plan
	var enabled int
	var createdAt string

	err := row.Scan(
		&p.ID, &p.Name, &p.Description, &p.Type, &p.PriceCents, &p.Currency,
		&p.StripePriceID, &p.TrafficLimit, &p.DurationDays,
		&p.DataLimitResetStrategy, &p.InboundIDs, &p.SortOrder, &enabled, &createdAt,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return plans.Plan{}, plans.ErrPlanNotFound
	}
	if err != nil {
		return plans.Plan{}, fmt.Errorf("scan plan: %w", err)
	}

	p.Enabled = enabled != 0
	if createdAt != "" {
		t, err := time.Parse(time.RFC3339Nano, createdAt)
		if err != nil {
			return plans.Plan{}, fmt.Errorf("parse plan created_at: %w", err)
		}
		p.CreatedAt = t
	}
	return p, nil
}
