package usage

import (
	"fmt"
	"time"

	"pulse/internal/nodes"
	"pulse/internal/users"
)

// OnlineThreshold 判断用户"在线"的时间窗口。
// SyncUsage 每分钟运行一次，3 分钟内有流量即视为在线。
const OnlineThreshold = 3 * time.Minute

// NodeStat 单个节点的流量统计。
type NodeStat struct {
	ID            string `json:"id"`
	Name          string `json:"name"`
	UploadBytes   int64  `json:"upload_bytes"`
	DownloadBytes int64  `json:"download_bytes"`
}

// DailyTrafficPoint 某日所有节点合并后的流量点（用于趋势图）。
type DailyTrafficPoint struct {
	Date          string  // YYYY-MM-DD
	Label         string  // 显示标签，如 "3/30"
	UploadBytes   int64
	DownloadBytes int64
	TotalBytes    int64
	HeightPct     float64 // 0–100，相对于窗口内最大值
}

// Summary 仪表盘统计摘要。
type Summary struct {
	// 节点
	NodesCount int        `json:"nodes_count"`
	NodeStats  []NodeStat `json:"node_stats"`

	// 用户（按有效状态分组）
	UsersCount          int `json:"users_count"`
	OnlineUsersCount    int `json:"online_users_count"`
	ActiveUsersCount    int `json:"active_users_count"`
	DisabledUsersCount  int `json:"disabled_users_count"`
	ExpiredUsersCount   int `json:"expired_users_count"`
	LimitedUsersCount   int `json:"limited_users_count"`
	ExpiringUsersCount  int `json:"expiring_users_count"` // 7 天内到期的活跃用户

	// 流量
	TotalUploadBytes   int64 `json:"total_upload_bytes"`
	TotalDownloadBytes int64 `json:"total_download_bytes"`
	TotalUsedBytes     int64 `json:"total_used_bytes"`

	// 近 14 天每日流量趋势
	DailyTraffic []DailyTrafficPoint `json:"daily_traffic"`
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

	// 流量总览从节点累计值汇总，不受用户流量重置影响
	for _, n := range nodesList {
		s.TotalUploadBytes += n.UploadBytes
		s.TotalDownloadBytes += n.DownloadBytes
	}
	s.TotalUsedBytes = s.TotalUploadBytes + s.TotalDownloadBytes

	now := time.Now()
	expiringDeadline := now.Add(7 * 24 * time.Hour)
	for _, u := range usersList {
		if u.OnlineAt != nil && now.Sub(*u.OnlineAt) <= OnlineThreshold {
			s.OnlineUsersCount++
		}
		switch u.EffectiveStatus() {
		case users.StatusActive:
			s.ActiveUsersCount++
			if u.ExpireAt != nil && u.ExpireAt.After(now) && u.ExpireAt.Before(expiringDeadline) {
				s.ExpiringUsersCount++
			}
		case users.StatusDisabled:
			s.DisabledUsersCount++
		case users.StatusExpired:
			s.ExpiredUsersCount++
		case users.StatusLimited:
			s.LimitedUsersCount++
		}
	}

	const dailyDays = 14
	dailyRaw, _ := nodeStore.ListNodeDailyUsage(dailyDays)
	s.DailyTraffic = aggregateDailyTraffic(dailyRaw, dailyDays)

	return s, nil
}

// aggregateDailyTraffic 将原始节点日记录按日期聚合，填充完整的 days 天窗口，并计算图表高度比例。
func aggregateDailyTraffic(raw []nodes.NodeDailyUsage, days int) []DailyTrafficPoint {
	byDate := make(map[string]*DailyTrafficPoint, days)
	for _, r := range raw {
		p, ok := byDate[r.Date]
		if !ok {
			t, _ := time.Parse("2006-01-02", r.Date)
			byDate[r.Date] = &DailyTrafficPoint{
				Date:  r.Date,
				Label: fmt.Sprintf("%d/%d", int(t.Month()), t.Day()),
			}
			p = byDate[r.Date]
		}
		p.UploadBytes += r.UploadBytes
		p.DownloadBytes += r.DownloadBytes
		p.TotalBytes += r.UploadBytes + r.DownloadBytes
	}

	result := make([]DailyTrafficPoint, days)
	now := time.Now().UTC()
	for i := range result {
		d := now.AddDate(0, 0, -(days - 1 - i))
		dateStr := d.Format("2006-01-02")
		if p, ok := byDate[dateStr]; ok {
			result[i] = *p
		} else {
			result[i] = DailyTrafficPoint{
				Date:  dateStr,
				Label: fmt.Sprintf("%d/%d", int(d.Month()), d.Day()),
			}
		}
	}

	var maxBytes int64
	for _, p := range result {
		if p.TotalBytes > maxBytes {
			maxBytes = p.TotalBytes
		}
	}
	if maxBytes > 0 {
		for i := range result {
			result[i].HeightPct = float64(result[i].TotalBytes) / float64(maxBytes) * 100
		}
	}

	return result
}
