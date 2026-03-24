package usage

import (
	"time"

	"pulse/internal/nodes"
	"pulse/internal/users"
)

type Summary struct {
	NodesCount         int            `json:"nodes_count"`
	UsersCount         int            `json:"users_count"`
	Protocols          map[string]int `json:"protocols"`
	TotalApplyCount    int            `json:"total_apply_count"`
	TotalUploadBytes   int64          `json:"total_upload_bytes"`
	TotalDownloadBytes int64          `json:"total_download_bytes"`
	TotalUsedBytes     int64          `json:"total_used_bytes"`
	LimitedUsersCount  int            `json:"limited_users_count"`
	DisabledUsersCount int            `json:"disabled_users_count"`
	LastAppliedAt      time.Time      `json:"last_applied_at,omitempty"`
}

func Build(nodeStore nodes.Store, userStore users.Store) (Summary, error) {
	nodesList, err := nodeStore.List()
	if err != nil {
		return Summary{}, err
	}

	usersList, err := userStore.List()
	if err != nil {
		return Summary{}, err
	}

	summary := Summary{
		NodesCount: len(nodesList),
		UsersCount: len(usersList),
		Protocols:  make(map[string]int),
	}

	for _, user := range usersList {
		protocol := user.Protocol
		if protocol == "" {
			protocol = "vless"
		}
		summary.Protocols[protocol]++
		summary.TotalApplyCount += user.ApplyCount
		summary.TotalUploadBytes += user.UploadBytes
		summary.TotalDownloadBytes += user.DownloadBytes
		summary.TotalUsedBytes += user.UsedBytes
		if user.TrafficLimit > 0 {
			summary.LimitedUsersCount++
		}
		if !user.EffectiveEnabled() {
			summary.DisabledUsersCount++
		}
		if user.LastAppliedAt.After(summary.LastAppliedAt) {
			summary.LastAppliedAt = user.LastAppliedAt
		}
	}

	return summary, nil
}
