package sqlite

import (
	"database/sql"
	"errors"
	"fmt"
	"time"

	"pulse/internal/orders"
)

type OrderStore struct {
	db *sql.DB
}

func (s *OrderStore) UpsertOrder(order orders.Order) (orders.Order, error) {
	if order.CreatedAt.IsZero() {
		order.CreatedAt = time.Now().UTC()
	}
	if order.Status == "" {
		order.Status = orders.StatusPending
	}
	if order.Currency == "" {
		order.Currency = "usd"
	}

	_, err := s.db.Exec(`
		INSERT INTO orders (
			id, user_id, plan_id, email, stripe_session_id,
			stripe_subscription_id, stripe_customer_id,
			status, amount_cents, currency, created_at, paid_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET
			user_id = excluded.user_id,
			plan_id = excluded.plan_id,
			email = excluded.email,
			stripe_session_id = excluded.stripe_session_id,
			stripe_subscription_id = excluded.stripe_subscription_id,
			stripe_customer_id = excluded.stripe_customer_id,
			status = excluded.status,
			amount_cents = excluded.amount_cents,
			currency = excluded.currency,
			created_at = excluded.created_at,
			paid_at = excluded.paid_at
	`,
		order.ID, order.UserID, order.PlanID, order.Email, order.StripeSessionID,
		order.StripeSubscriptionID, order.StripeCustomerID,
		order.Status, order.AmountCents, order.Currency,
		order.CreatedAt.Format(time.RFC3339Nano), formatTimePtr(order.PaidAt),
	)
	if err != nil {
		return orders.Order{}, fmt.Errorf("upsert order: %w", err)
	}
	return order, nil
}

func (s *OrderStore) GetOrder(id string) (orders.Order, error) {
	row := s.db.QueryRow(`
		SELECT id, user_id, plan_id, email, stripe_session_id,
		       stripe_subscription_id, stripe_customer_id,
		       status, amount_cents, currency, created_at, paid_at
		FROM orders WHERE id = ?
	`, id)
	return scanOrder(row)
}

func (s *OrderStore) GetOrderByStripeSession(sessionID string) (orders.Order, error) {
	row := s.db.QueryRow(`
		SELECT id, user_id, plan_id, email, stripe_session_id,
		       stripe_subscription_id, stripe_customer_id,
		       status, amount_cents, currency, created_at, paid_at
		FROM orders WHERE stripe_session_id = ?
	`, sessionID)
	return scanOrder(row)
}

func (s *OrderStore) GetOrderByStripeSubscription(subscriptionID string) (orders.Order, error) {
	row := s.db.QueryRow(`
		SELECT id, user_id, plan_id, email, stripe_session_id,
		       stripe_subscription_id, stripe_customer_id,
		       status, amount_cents, currency, created_at, paid_at
		FROM orders WHERE stripe_subscription_id = ?
	`, subscriptionID)
	return scanOrder(row)
}

func (s *OrderStore) ListOrders() ([]orders.Order, error) {
	rows, err := s.db.Query(`
		SELECT id, user_id, plan_id, email, stripe_session_id,
		       stripe_subscription_id, stripe_customer_id,
		       status, amount_cents, currency, created_at, paid_at
		FROM orders ORDER BY created_at DESC
	`)
	if err != nil {
		return nil, fmt.Errorf("list orders: %w", err)
	}
	defer rows.Close()
	return scanOrders(rows)
}

func (s *OrderStore) ListOrdersByUser(userID string) ([]orders.Order, error) {
	rows, err := s.db.Query(`
		SELECT id, user_id, plan_id, email, stripe_session_id,
		       stripe_subscription_id, stripe_customer_id,
		       status, amount_cents, currency, created_at, paid_at
		FROM orders WHERE user_id = ? ORDER BY created_at DESC
	`, userID)
	if err != nil {
		return nil, fmt.Errorf("list orders by user: %w", err)
	}
	defer rows.Close()
	return scanOrders(rows)
}

func (s *OrderStore) ListOrdersByEmail(email string) ([]orders.Order, error) {
	rows, err := s.db.Query(`
		SELECT id, user_id, plan_id, email, stripe_session_id,
		       stripe_subscription_id, stripe_customer_id,
		       status, amount_cents, currency, created_at, paid_at
		FROM orders WHERE email = ? ORDER BY created_at DESC
	`, email)
	if err != nil {
		return nil, fmt.Errorf("list orders by email: %w", err)
	}
	defer rows.Close()
	return scanOrders(rows)
}

func (s *OrderStore) DeleteOrder(id string) error {
	result, err := s.db.Exec(`DELETE FROM orders WHERE id = ?`, id)
	if err != nil {
		return fmt.Errorf("delete order: %w", err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("delete order rows affected: %w", err)
	}
	if affected == 0 {
		return orders.ErrOrderNotFound
	}
	return nil
}

// ─── 扫描辅助 ─────────────────────────────────────────────────────────────────

func scanOrders(rows *sql.Rows) ([]orders.Order, error) {
	items := make([]orders.Order, 0)
	for rows.Next() {
		o, err := scanOrder(rows)
		if err != nil {
			return nil, err
		}
		items = append(items, o)
	}
	return items, rows.Err()
}

func scanOrder(row scanner) (orders.Order, error) {
	var o orders.Order
	var createdAt string
	var paidAt sql.NullString

	err := row.Scan(
		&o.ID, &o.UserID, &o.PlanID, &o.Email, &o.StripeSessionID,
		&o.StripeSubscriptionID, &o.StripeCustomerID,
		&o.Status, &o.AmountCents, &o.Currency, &createdAt, &paidAt,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return orders.Order{}, orders.ErrOrderNotFound
	}
	if err != nil {
		return orders.Order{}, fmt.Errorf("scan order: %w", err)
	}

	if createdAt != "" {
		t, err := time.Parse(time.RFC3339Nano, createdAt)
		if err != nil {
			return orders.Order{}, fmt.Errorf("parse order created_at: %w", err)
		}
		o.CreatedAt = t
	}
	if paidAt.Valid && paidAt.String != "" {
		t, err := time.Parse(time.RFC3339Nano, paidAt.String)
		if err != nil {
			return orders.Order{}, fmt.Errorf("parse order paid_at: %w", err)
		}
		o.PaidAt = &t
	}
	return o, nil
}
