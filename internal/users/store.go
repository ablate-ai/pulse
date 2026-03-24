package users

import (
	"errors"
	"time"
)

var ErrUserNotFound = errors.New("user not found")

type User struct {
	ID                  string    `json:"id"`
	Username            string    `json:"username"`
	UUID                string    `json:"uuid"`
	Protocol            string    `json:"protocol"`
	Secret              string    `json:"secret,omitempty"`
	Method              string    `json:"method,omitempty"`
	Enabled             bool      `json:"enabled"`
	NodeID              string    `json:"node_id"`
	Domain              string    `json:"domain"`
	Port                int       `json:"port"`
	InboundTag          string    `json:"inbound_tag"`
	TrafficLimit        int64     `json:"traffic_limit_bytes"`
	UploadBytes         int64     `json:"upload_bytes"`
	DownloadBytes       int64     `json:"download_bytes"`
	UsedBytes           int64     `json:"used_bytes"`
	SyncedUploadBytes   int64     `json:"-"`
	SyncedDownloadBytes int64     `json:"-"`
	ApplyCount          int       `json:"apply_count"`
	LastAppliedAt       time.Time `json:"last_applied_at,omitempty"`
	CreatedAt           time.Time `json:"created_at"`
}

type Store interface {
	Upsert(user User) (User, error)
	Get(id string) (User, error)
	List() ([]User, error)
	ListByNode(nodeID string) ([]User, error)
	Delete(id string) error
}

func (u User) EffectiveEnabled() bool {
	if u.TrafficLimit > 0 && u.UsedBytes >= u.TrafficLimit {
		return false
	}
	return u.Enabled
}
