package plans

import (
	"errors"
	"time"
)

var ErrPlanNotFound = errors.New("plan not found")

// Plan 定义一个可购买的套餐。
type Plan struct {
	ID                     string    `json:"id"`
	Name                   string    `json:"name"`
	Description            string    `json:"description"`
	Type                   string    `json:"type"` // "subscription" | "one_time"
	PriceCents             int       `json:"price_cents"`
	Currency               string    `json:"currency"`
	StripePriceID          string    `json:"stripe_price_id"`
	TrafficLimit           int64     `json:"traffic_limit"`
	DurationDays           int       `json:"duration_days"`
	DataLimitResetStrategy string    `json:"data_limit_reset_strategy"`
	InboundIDs             string    `json:"inbound_ids"` // 逗号分隔
	SortOrder              int       `json:"sort_order"`
	Enabled                bool      `json:"enabled"`
	CreatedAt              time.Time `json:"created_at"`
}

const (
	TypeSubscription = "subscription"
	TypeOneTime      = "one_time"
)

// Store 套餐持久化接口。
type Store interface {
	UpsertPlan(plan Plan) (Plan, error)
	GetPlan(id string) (Plan, error)
	ListPlans() ([]Plan, error)
	ListEnabledPlans() ([]Plan, error)
	DeletePlan(id string) error
}
