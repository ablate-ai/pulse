package users

import (
	"errors"
	"time"
)

var (
	ErrUserNotFound        = errors.New("user not found")
	ErrUserInboundNotFound = errors.New("user inbound not found")
)

const (
	StatusActive   = "active"
	StatusDisabled = "disabled"
	StatusLimited  = "limited"
	StatusExpired  = "expired"
	StatusOnHold   = "on_hold"

	ResetStrategyNoReset = "no_reset"
	ResetStrategyDay     = "day"
	ResetStrategyWeek    = "week"
	ResetStrategyMonth   = "month"
	ResetStrategyYear    = "year"
)

// User 用户身份与流量统计。
type User struct {
	ID                     string     `json:"id"`
	Username               string     `json:"username"`
	Status                 string     `json:"status"`
	Note                   string     `json:"note,omitempty"`
	ExpireAt               *time.Time `json:"expire_at,omitempty"`
	DataLimitResetStrategy string     `json:"data_limit_reset_strategy"`
	TrafficLimit           int64      `json:"traffic_limit_bytes"`
	UploadBytes            int64      `json:"upload_bytes"`
	DownloadBytes          int64      `json:"download_bytes"`
	UsedBytes              int64      `json:"used_bytes"`
	OnHoldExpireAt         *time.Time `json:"on_hold_expire_at,omitempty"`
	LastTrafficResetAt     *time.Time `json:"last_traffic_reset_at,omitempty"`
	OnlineAt               *time.Time `json:"online_at,omitempty"`
	Connections            int        `json:"connections"`
	Devices                int        `json:"devices"`
	CreatedAt              time.Time  `json:"created_at"`
	SubToken               string     `json:"sub_token,omitempty"`
}

// UserInbound 用户在某个节点上的访问凭据（一条记录对应一个 (user_id, node_id) 对）。
// 协议配置由节点的 inbounds.Inbound 定义，此处只存储凭据和流量同步游标。
type UserInbound struct {
	ID                  string    `json:"id"`
	UserID              string    `json:"user_id"`
	NodeID              string    `json:"node_id"`
	UUID                string    `json:"uuid"`   // 用于 VLESS / VMess
	Secret              string    `json:"secret"` // 用于 Trojan / Shadowsocks
	SyncedUploadBytes   int64     `json:"-"`
	SyncedDownloadBytes int64     `json:"-"`
	CreatedAt           time.Time `json:"created_at"`
}

// Store 用户和入站数据的持久化接口。
type Store interface {
	// User CRUD
	UpsertUser(user User) (User, error)
	GetUser(id string) (User, error)
	GetUserBySubToken(token string) (User, error)
	ListUsers() ([]User, error)
	DeleteUser(id string) error

	// UserInbound CRUD（每个用户在每个节点上只有一条凭据记录）
	UpsertUserInbound(inbound UserInbound) (UserInbound, error)
	GetUserInbound(id string) (UserInbound, error)
	ListUserInboundsByUser(userID string) ([]UserInbound, error)
	ListUserInboundsByNode(nodeID string) ([]UserInbound, error)
	DeleteUserInbound(id string) error
	DeleteUserInboundsByUser(userID string) error

	// GetUsersByIDs 批量获取 User，返回 map[userID]User。
	GetUsersByIDs(ids []string) (map[string]User, error)
}

// EffectiveStatus 计算用户的实际运行时状态（不写库，仅计算）。
func (u User) EffectiveStatus() string {
	if u.Status == StatusDisabled {
		return u.Status
	}
	if u.Status == StatusOnHold {
		// OnHoldExpireAt 到期则自动视为 active（实际状态更新由 job 负责）
		if u.OnHoldExpireAt != nil && !u.OnHoldExpireAt.IsZero() && time.Now().After(*u.OnHoldExpireAt) {
			return StatusActive
		}
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
