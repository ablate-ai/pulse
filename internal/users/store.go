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
	RawUploadBytes         int64      `json:"raw_upload_bytes"`   // 实际上行流量（不含倍率）
	RawDownloadBytes       int64      `json:"raw_download_bytes"` // 实际下行流量（不含倍率）
	OnHoldExpireAt         *time.Time `json:"on_hold_expire_at,omitempty"`
	LastTrafficResetAt     *time.Time `json:"last_traffic_reset_at,omitempty"`
	OnlineAt               *time.Time `json:"online_at,omitempty"`
	Connections            int        `json:"connections"`
	Devices                int        `json:"devices"`
	CreatedAt              time.Time  `json:"created_at"`
	SubToken               string     `json:"sub_token,omitempty"`
}

// UserInbound 用户对某个具体 inbound 的访问凭据（一条记录对应一个 (user_id, inbound_id) 对）。
// NodeID 从 Inbound 反推，保留用于流量聚合查询。
// 协议配置由节点的 inbounds.Inbound 定义，此处只存储凭据。
type UserInbound struct {
	ID        string    `json:"id"`
	UserID    string    `json:"user_id"`
	InboundID string    `json:"inbound_id"` // 对应具体的 inbound
	NodeID    string    `json:"node_id"`    // 冗余字段，用于流量聚合
	UUID      string    `json:"uuid"`       // 用于 VLESS / VMess
	Secret    string    `json:"secret"`     // 用于 Trojan / Shadowsocks
	CreatedAt time.Time `json:"created_at"`
}

// SubAccessLog 记录一次订阅拉取行为。
type SubAccessLog struct {
	ID         int64     `json:"id"`
	UserID     string    `json:"user_id"`
	IP         string    `json:"ip"`
	UserAgent  string    `json:"user_agent"`
	AccessedAt time.Time `json:"accessed_at"`
}

// Store 用户和入站数据的持久化接口。
type Store interface {
	// User CRUD
	UpsertUser(user User) (User, error)
	GetUser(id string) (User, error)
	GetUserBySubToken(token string) (User, error)
	ListUsers() ([]User, error)
	DeleteUser(id string) error

	// UserInbound CRUD（每个用户在每个 inbound 上只有一条凭据记录）
	UpsertUserInbound(inbound UserInbound) (UserInbound, error)
	GetUserInbound(id string) (UserInbound, error)
	ListUserInboundsByUser(userID string) ([]UserInbound, error)
	ListUserInboundsByNode(nodeID string) ([]UserInbound, error)
	ListUserInboundsByInbound(inboundID string) ([]UserInbound, error)
	DeleteUserInbound(id string) error
	DeleteUserInboundsByUser(userID string) error

	// GetUsersByIDs 批量获取 User，返回 map[userID]User。
	GetUsersByIDs(ids []string) (map[string]User, error)

	// 订阅访问日志
	LogSubAccess(userID, ip, userAgent string) error
	ListSubAccessLogs(userID string, limit int) ([]SubAccessLog, error)

	// 用户节点流量统计
	AddUserNodeTraffic(userID, nodeID, date string, upload, download int64) error
	ListUserNodeUsage(userID string) ([]UserNodeUsage, error)

	// ListUserDailyUsage 返回用户近 days 天的每日流量（跨节点合并）。
	ListUserDailyUsage(userID string, days int) ([]UserDailyUsage, error)
}

// UserNodeUsage 某用户在某节点的累计流量（所有日期汇总）。
type UserNodeUsage struct {
	NodeID        string
	UploadBytes   int64
	DownloadBytes int64
}

// UserDailyUsage 某用户某天的合并流量（跨节点求和）。
type UserDailyUsage struct {
	Date          string // YYYY-MM-DD
	UploadBytes   int64
	DownloadBytes int64
}

// EffectiveStatusAt 使用给定时间计算用户的实际运行时状态（不写库，仅计算）。
// 在同一同步周期内传入相同的 now 可保证结果确定。
func (u User) EffectiveStatusAt(now time.Time) string {
	if u.Status == StatusDisabled {
		return u.Status
	}
	if u.Status == StatusOnHold {
		// OnHoldExpireAt 到期则自动视为 active（实际状态更新由 job 负责）
		if u.OnHoldExpireAt != nil && !u.OnHoldExpireAt.IsZero() && now.After(*u.OnHoldExpireAt) {
			return StatusActive
		}
		return u.Status
	}
	if u.ExpireAt != nil && !u.ExpireAt.IsZero() && now.After(*u.ExpireAt) {
		return StatusExpired
	}
	if u.TrafficLimit > 0 && u.UsedBytes >= u.TrafficLimit {
		return StatusLimited
	}
	return StatusActive
}

// EffectiveStatus 计算用户的实际运行时状态（便捷方法，使用当前时间）。
func (u User) EffectiveStatus() string {
	return u.EffectiveStatusAt(time.Now())
}

// EffectiveEnabledAt 使用给定时间判断用户是否应被下发到节点。
func (u User) EffectiveEnabledAt(now time.Time) bool {
	return u.EffectiveStatusAt(now) == StatusActive
}

// EffectiveEnabled 判断用户是否应被下发到节点（便捷方法，使用当前时间）。
func (u User) EffectiveEnabled() bool {
	return u.EffectiveStatus() == StatusActive
}
