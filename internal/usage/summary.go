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

// NodePeriodStat 节点在选定时段内的流量（来自 daily_usage 表，不受用户重置影响）。
type NodePeriodStat struct {
	ID            string `json:"id"`
	Name          string `json:"name"`
	UploadBytes   int64  `json:"upload_bytes"`
	DownloadBytes int64  `json:"download_bytes"`
	TotalBytes    int64  `json:"total_bytes"`
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

	// 每日流量趋势（天数由 days 参数控制）
	DailyTraffic []DailyTrafficPoint `json:"daily_traffic"`

	// 选定时段内各节点流量对比
	NodePeriodStats []NodePeriodStat `json:"node_period_stats"`

	// 当前选中的时间范围（天数）
	Days        int   `json:"days"`
	DaysOptions []int `json:"days_options"`
}

func Build(nodeStore nodes.Store, userStore users.Store, days int) (Summary, error) {
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

	if days <= 0 {
		days = 14
	}

	s := Summary{
		NodesCount:  len(nodesList),
		NodeStats:   nodeStats,
		UsersCount:  len(usersList),
		Days:        days,
		DaysOptions: []int{7, 14, 30, 90},
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

	dailyRaw, _ := nodeStore.ListNodeDailyUsage(days)
	s.DailyTraffic = aggregateDailyTraffic(dailyRaw, days)
	s.NodePeriodStats = aggregateNodePeriodStats(dailyRaw, nodesList)

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

// aggregateNodePeriodStats 按节点汇总时段内的流量增量（来自 daily_usage 原始记录）。
func aggregateNodePeriodStats(raw []nodes.NodeDailyUsage, nodeList []nodes.Node) []NodePeriodStat {
	byNode := make(map[string]*NodePeriodStat, len(nodeList))
	for _, n := range nodeList {
		ns := &NodePeriodStat{ID: n.ID, Name: n.Name}
		byNode[n.ID] = ns
	}
	for _, r := range raw {
		if ns, ok := byNode[r.NodeID]; ok {
			ns.UploadBytes += r.UploadBytes
			ns.DownloadBytes += r.DownloadBytes
			ns.TotalBytes += r.UploadBytes + r.DownloadBytes
		}
	}
	result := make([]NodePeriodStat, 0, len(nodeList))
	for _, n := range nodeList {
		result = append(result, *byNode[n.ID])
	}
	return result
}
