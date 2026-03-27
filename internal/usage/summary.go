package usage

import (
	"pulse/internal/nodes"
	"pulse/internal/users"
)

// NodeStat 单个节点的流量统计。
type NodeStat struct {
	ID            string `json:"id"`
	Name          string `json:"name"`
	UploadBytes   int64  `json:"upload_bytes"`
	DownloadBytes int64  `json:"download_bytes"`
}

// Summary 仪表盘统计摘要。
type Summary struct {
	// 节点
	NodesCount int        `json:"nodes_count"`
	NodeStats  []NodeStat `json:"node_stats"`

	// 用户（按有效状态分组）
	UsersCount         int `json:"users_count"`
	ActiveUsersCount   int `json:"active_users_count"`
	DisabledUsersCount int `json:"disabled_users_count"`
	ExpiredUsersCount  int `json:"expired_users_count"`
	LimitedUsersCount  int `json:"limited_users_count"`

	// 流量
	TotalUploadBytes   int64 `json:"total_upload_bytes"`
	TotalDownloadBytes int64 `json:"total_download_bytes"`
	TotalUsedBytes     int64 `json:"total_used_bytes"`
}

func Build(nodeStore nodes.Store, userStore users.Store) (Summary, error) {
	nodesList, err := nodeStore.List()
	if err != nil {
		return Summary{}, err
	}

	usersList, err := userStore.ListUsers()
	if err != nil {
		return Summary{}, err
	}

	nodeStats := make([]NodeStat, 0, len(nodesList))
	for _, n := range nodesList {
		nodeStats = append(nodeStats, NodeStat{
			ID:            n.ID,
			Name:          n.Name,
			UploadBytes:   n.UploadBytes,
			DownloadBytes: n.DownloadBytes,
		})
	}

	s := Summary{
		NodesCount: len(nodesList),
		NodeStats:  nodeStats,
		UsersCount: len(usersList),
	}

	for _, u := range usersList {
		s.TotalUploadBytes += u.UploadBytes
		s.TotalDownloadBytes += u.DownloadBytes
		s.TotalUsedBytes += u.UsedBytes

		switch u.EffectiveStatus() {
		case users.StatusActive:
			s.ActiveUsersCount++
		case users.StatusDisabled:
			s.DisabledUsersCount++
		case users.StatusExpired:
			s.ExpiredUsersCount++
		case users.StatusLimited:
			s.LimitedUsersCount++
		}
	}

	return s, nil
}
