package usage

import (
	"fmt"
	"log"
	"sort"
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

// NodeCombinedStat 节点流量合并统计：累计值（来自 nodes 表）+ 时段增量（来自 daily_usage）。
type NodeCombinedStat struct {
	ID                  string `json:"id"`
	Name                string `json:"name"`
	TotalUploadBytes    int64  `json:"total_upload_bytes"`
	TotalDownloadBytes  int64  `json:"total_download_bytes"`
	PeriodUploadBytes   int64  `json:"period_upload_bytes"`
	PeriodDownloadBytes int64  `json:"period_download_bytes"`
	PeriodTotalBytes    int64  `json:"period_total_bytes"`
}

// ExpirationDayPoint 某天到期的用户数（未来30天时间线图）。
type ExpirationDayPoint struct {
	Date  string
	Label string
	Count int
}

// UserGrowthPoint 某天新增用户数（用户增长趋势图）。
type UserGrowthPoint struct {
	Date  string
	Label string
	Count int
}

// TopUserStat 流量排行用户（Top 10）。
type TopUserStat struct {
	Username     string
	UsedBytes    int64 // 计费流量（含节点倍率）
	RawUsedBytes int64 // 实际流量（不含倍率）
	TrafficLimit int64
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

	// 连接
	TotalConnections int `json:"total_connections"`
	TotalDevices     int `json:"total_devices"`

	// 流量
	TotalUploadBytes   int64 `json:"total_upload_bytes"`
	TotalDownloadBytes int64 `json:"total_download_bytes"`
	TotalUsedBytes     int64 `json:"total_used_bytes"`

	// 每日流量趋势（天数由 days 参数控制）
	DailyTraffic []DailyTrafficPoint `json:"daily_traffic"`

	// 选定时段内各节点流量对比
	NodePeriodStats []NodePeriodStat `json:"node_period_stats"`

	// 累计 + 时段增量合并（面板节点流量区块使用）
	NodeCombinedStats []NodeCombinedStat `json:"node_combined_stats"`

	// 当前选中的时间范围（天数）
	Days        int   `json:"days"`
	DaysOptions []int `json:"days_options"`

	// 运营报表扩展数据
	ExpirationDays []ExpirationDayPoint `json:"expiration_days"`  // 未来 30 天到期时间线
	UserGrowth     []UserGrowthPoint    `json:"user_growth"`      // 近 days 天用户增长
	TopUsers       []TopUserStat        `json:"top_users"`        // 流量 Top 10
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
		s.TotalConnections += u.Connections
		s.TotalDevices += u.Devices
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

	dailyRaw, err := nodeStore.ListNodeDailyUsage(days)
	if err != nil {
		log.Printf("usage.Build: ListNodeDailyUsage: %v", err)
	}
	s.DailyTraffic = aggregateDailyTraffic(dailyRaw, days)
	s.NodePeriodStats = aggregateNodePeriodStats(dailyRaw, nodesList)

	periodByID := make(map[string]NodePeriodStat, len(s.NodePeriodStats))
	for _, p := range s.NodePeriodStats {
		periodByID[p.ID] = p
	}
	combined := make([]NodeCombinedStat, 0, len(nodeStats))
	for _, ns := range nodeStats {
		p := periodByID[ns.ID]
		combined = append(combined, NodeCombinedStat{
			ID:                  ns.ID,
			Name:                ns.Name,
			TotalUploadBytes:    ns.UploadBytes,
			TotalDownloadBytes:  ns.DownloadBytes,
			PeriodUploadBytes:   p.UploadBytes,
			PeriodDownloadBytes: p.DownloadBytes,
			PeriodTotalBytes:    p.TotalBytes,
		})
	}
	s.NodeCombinedStats = combined

	// --- 未来 30 天到期时间线 ---
	expMap := make(map[string]int, 30)
	for _, u := range usersList {
		if u.ExpireAt == nil {
			continue
		}
		exp := u.ExpireAt.UTC()
		if exp.Before(now.UTC()) || exp.After(now.UTC().AddDate(0, 0, 30)) {
			continue
		}
		expMap[exp.Format("2006-01-02")]++
	}
	for i := 0; i < 30; i++ {
		d := now.UTC().AddDate(0, 0, i)
		date := d.Format("2006-01-02")
		s.ExpirationDays = append(s.ExpirationDays, ExpirationDayPoint{
			Date:  date,
			Label: fmt.Sprintf("%d/%d", int(d.Month()), d.Day()),
			Count: expMap[date],
		})
	}

	// --- 近 days 天用户增长趋势 ---
	growthMap := make(map[string]int, days)
	for _, u := range usersList {
		growthMap[u.CreatedAt.UTC().Format("2006-01-02")]++
	}
	for i := 0; i < days; i++ {
		d := now.UTC().AddDate(0, 0, -(days - 1 - i))
		date := d.Format("2006-01-02")
		s.UserGrowth = append(s.UserGrowth, UserGrowthPoint{
			Date:  date,
			Label: fmt.Sprintf("%d/%d", int(d.Month()), d.Day()),
			Count: growthMap[date],
		})
	}

	// --- 流量 Top 10 用户 ---
	sorted := make([]users.User, len(usersList))
	copy(sorted, usersList)
	sort.Slice(sorted, func(i, j int) bool {
		return sorted[i].UsedBytes > sorted[j].UsedBytes
	})
	for _, u := range sorted {
		if len(s.TopUsers) >= 10 || u.UsedBytes == 0 {
			break
		}
		s.TopUsers = append(s.TopUsers, TopUserStat{
			Username:     u.Username,
			UsedBytes:    u.UsedBytes,
			RawUsedBytes: u.RawUploadBytes + u.RawDownloadBytes,
			TrafficLimit: u.TrafficLimit,
		})
	}

	return s, nil
}

// AggregateDailyTraffic 将原始节点日记录按日期聚合，填充完整的 days 天窗口，并计算图表高度比例。
// 调用前可按 NodeID 过滤 raw，以获得单节点趋势。
func AggregateDailyTraffic(raw []nodes.NodeDailyUsage, days int) []DailyTrafficPoint {
	return aggregateDailyTraffic(raw, days)
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
