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
	ExpireAt               *time.Time `json:"expire_at,omitempty"`
	DataLimitResetStrategy string     `json:"data_limit_reset_strategy"`
	TrafficLimit           int64      `json:"traffic_limit_bytes"`
	UploadBytes            int64      `json:"upload_bytes"`
	DownloadBytes          int64      `json:"download_bytes"`
	UsedBytes              int64      `json:"used_bytes"`
	LastTrafficResetAt     *time.Time `json:"last_traffic_reset_at,omitempty"`
	CreatedAt              time.Time  `json:"created_at"`
}

// UserInbound 用户在某个节点上的一条协议入站配置。
type UserInbound struct {
	ID                   string    `json:"id"`
	UserID               string    `json:"user_id"`
	NodeID               string    `json:"node_id"`
	Protocol             string    `json:"protocol"`
	UUID                 string    `json:"uuid"`
	Secret               string    `json:"secret,omitempty"`
	Method               string    `json:"method,omitempty"`
	Security             string    `json:"security,omitempty"`
	Flow                 string    `json:"flow,omitempty"`
	SNI                  string    `json:"sni,omitempty"`
	Fingerprint          string    `json:"fingerprint,omitempty"`
	RealityPublicKey     string    `json:"reality_public_key,omitempty"`
	RealityShortID       string    `json:"reality_short_id,omitempty"`
	RealitySpiderX       string    `json:"reality_spider_x,omitempty"`
	RealityPrivateKey    string    `json:"reality_private_key,omitempty"`
	RealityHandshakeAddr string    `json:"reality_handshake_addr,omitempty"`
	Domain               string    `json:"domain"`
	Port                 int       `json:"port"`
	InboundTag           string    `json:"inbound_tag"`
	// 不入库，apply 时由节点填充
	TLSCertPath  string `json:"-"`
	TLSKeyPath   string `json:"-"`
	// 流量同步游标（per-user per-node，SyncUsage 时去重）
	SyncedUploadBytes   int64     `json:"-"`
	SyncedDownloadBytes int64     `json:"-"`
	ApplyCount          int       `json:"apply_count"`
	LastAppliedAt       time.Time `json:"last_applied_at,omitempty"`
	CreatedAt           time.Time `json:"created_at"`
}

// Store 用户和入站数据的持久化接口。
type Store interface {
	// User CRUD
	UpsertUser(user User) (User, error)
	GetUser(id string) (User, error)
	ListUsers() ([]User, error)
	DeleteUser(id string) error

	// UserInbound CRUD
	UpsertUserInbound(inbound UserInbound) (UserInbound, error)
	GetUserInbound(id string) (UserInbound, error)
	ListUserInbounds() ([]UserInbound, error)
	ListUserInboundsByUser(userID string) ([]UserInbound, error)
	ListUserInboundsByNode(nodeID string) ([]UserInbound, error)
	// ListCursorInboundsByNode 返回每个 (userID, nodeID) 组合中 ID 最小的 inbound（流量游标持有者）。
	ListCursorInboundsByNode(nodeID string) ([]UserInbound, error)
	DeleteUserInbound(id string) error
	DeleteUserInboundsByUser(userID string) error
	// GetUsersByIDs 批量获取 User，返回 map[userID]User。
	GetUsersByIDs(ids []string) (map[string]User, error)
}

// EffectiveStatus 计算用户的实际运行时状态（不写库，仅计算）。
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
