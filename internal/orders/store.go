package orders

import (
	"errors"
	"time"
)

var ErrOrderNotFound = errors.New("order not found")

const (
	StatusPending  = "pending"
	StatusPaid     = "paid"
	StatusFailed   = "failed"
	StatusRefunded = "refunded"
)

// Order 记录一笔支付订单。
type Order struct {
	ID                   string     `json:"id"`
	UserID               string     `json:"user_id"`
	PlanID               string     `json:"plan_id"`
	Email                string     `json:"email"`
	StripeSessionID      string     `json:"stripe_session_id"`
	StripeSubscriptionID string     `json:"stripe_subscription_id"`
	StripeCustomerID     string     `json:"stripe_customer_id"`
	Status               string     `json:"status"`
	AmountCents          int        `json:"amount_cents"`
	Currency             string     `json:"currency"`
	CreatedAt            time.Time  `json:"created_at"`
	PaidAt               *time.Time `json:"paid_at"`
}

// Store 订单持久化接口。
type Store interface {
	UpsertOrder(order Order) (Order, error)
	GetOrder(id string) (Order, error)
	GetOrderByStripeSession(sessionID string) (Order, error)
	GetOrderByStripeSubscription(subscriptionID string) (Order, error)
	ListOrders() ([]Order, error)
	ListOrdersByUser(userID string) ([]Order, error)
	ListOrdersByEmail(email string) ([]Order, error)
	DeleteOrder(id string) error
}
