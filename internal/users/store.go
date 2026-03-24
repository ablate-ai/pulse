package users

import (
	"errors"
	"time"
)

var ErrUserNotFound = errors.New("user not found")

// 用户状态常量
const (
	StatusActive   = "active"
	StatusDisabled = "disabled"
	StatusLimited  = "limited"
	StatusExpired  = "expired"
	StatusOnHold   = "on_hold"
)

// 流量重置策略常量
const (
	ResetStrategyNoReset = "no_reset"
	ResetStrategyDay     = "day"
	ResetStrategyWeek    = "week"
	ResetStrategyMonth   = "month"
	ResetStrategyYear    = "year"
)

type User struct {
	ID                    string     `json:"id"`
	Username              string     `json:"username"`
	UUID                  string     `json:"uuid"`
	Protocol              string     `json:"protocol"`
	Secret                string     `json:"secret,omitempty"`
	Method                string     `json:"method,omitempty"`
	Status                string     `json:"status"`
	ExpireAt              *time.Time `json:"expire_at,omitempty"`
	DataLimitResetStrategy string    `json:"data_limit_reset_strategy"`
	NodeID                string     `json:"node_id"`
	Domain                string     `json:"domain"`
	Port                  int        `json:"port"`
	InboundTag            string     `json:"inbound_tag"`
	TrafficLimit          int64      `json:"traffic_limit_bytes"`
	UploadBytes           int64      `json:"upload_bytes"`
	DownloadBytes         int64      `json:"download_bytes"`
	UsedBytes             int64      `json:"used_bytes"`
	SyncedUploadBytes     int64      `json:"-"`
	SyncedDownloadBytes   int64      `json:"-"`
	ApplyCount            int        `json:"apply_count"`
	LastAppliedAt         time.Time  `json:"last_applied_at,omitempty"`
	LastTrafficResetAt    *time.Time `json:"last_traffic_reset_at,omitempty"`
	CreatedAt             time.Time  `json:"created_at"`
}

type Store interface {
	Upsert(user User) (User, error)
	Get(id string) (User, error)
	List() ([]User, error)
	ListByNode(nodeID string) ([]User, error)
	Delete(id string) error
}

// EffectiveStatus 计算用户的实际运行时状态（不写库，仅计算）。
// 管理员手动设置的 disabled/on_hold 优先级最高；
// 其次按过期时间和流量用量自动判断。
func (u User) EffectiveStatus() string {
	if u.Status == StatusDisabled || u.Status == StatusOnHold {
		return u.Status
	}
	if u.ExpireAt != nil && !u.ExpireAt.IsZero() && time.Now().After(*u.ExpireAt) {
		return StatusExpired
	}
	if u.TrafficLimit > 0 && u.UsedBytes >= u.TrafficLimit {
		return StatusLimited
	}
	return StatusActive
}

// EffectiveEnabled 判断用户是否应被下发到节点。
func (u User) EffectiveEnabled() bool {
	return u.EffectiveStatus() == StatusActive
}
