package jobs

import (
	"context"
	"fmt"
	"log"
	"time"

	"pulse/internal/inbounds"
	"pulse/internal/nodes"
	"pulse/internal/outbounds"
	"pulse/internal/proxycfg"
	"pulse/internal/routerules"
	"pulse/internal/users"
)

// NodeDialer 根据节点 ID 返回 RPC 客户端。
type NodeDialer func(nodeID string) (*nodes.Client, error)

// ─── SyncUsage ────────────────────────────────────────────────────────────────

// SyncUsageResult 记录一次同步的结果摘要。
type SyncUsageResult struct {
	NodesSynced   int      `json:"nodes_synced"`
	UsersUpdated  int      `json:"users_updated"`
	NodesReloaded int      `json:"nodes_reloaded"`
	NodesStopped  int      `json:"nodes_stopped"`
	Errors        []string `json:"errors"`
}

// SyncUsage 从各节点拉取流量统计，更新用户字节数，
// 若某节点上的用户启用状态发生变化则重新下发配置。
func SyncUsage(ctx context.Context, store users.Store, nodeStore nodes.Store, ibStore inbounds.InboundStore, dial NodeDialer, applyOpts ApplyOptions, outboundStore outbounds.Store) (SyncUsageResult, error) {
	nodesList, err := nodeStore.List()
	if err != nil {
		return SyncUsageResult{}, err
	}

	result := SyncUsageResult{Errors: make([]string, 0)}
	// 记录本轮已首次处理的用户，确保连接数从零开始累加而非叠加上轮旧值
	connResetUsers := make(map[string]struct{})
	// 同一 sync 周期使用统一时间，避免 EffectiveStatus 边界抖动
	now := time.Now().UTC()

	for _, node := range nodesList {
		client, err := dial(node.ID)
		if err != nil {
			result.Errors = append(result.Errors, node.ID+": "+err.Error())
			continue
		}

		usage, err := client.Usage(ctx, true)
		if err != nil {
			result.Errors = append(result.Errors, node.ID+": "+err.Error())
			sendAlert(ctx, applyOpts.Alerter, "节点离线", fmt.Sprintf("无法连接节点 %s", node.Name))
			continue
		}
		if !usage.Available {
			result.Errors = append(result.Errors, node.ID+": V2Ray Stats not available")
			// sing-box 进程未运行（非 idle 配置）时，主动下发配置恢复
			if !usage.Running {
				sendAlert(ctx, applyOpts.Alerter, "节点异常", fmt.Sprintf("节点 %s sing-box 停止运行", node.Name))
				nodeInbounds, err := ibStore.ListInboundsByNode(node.ID)
				if err != nil {
					result.Errors = append(result.Errors, node.ID+": recovery load inbounds: "+err.Error())
					continue
				}
				recoveryAccesses, err := store.ListUserInboundsByNode(node.ID)
				if err != nil {
					result.Errors = append(result.Errors, node.ID+": recovery load users: "+err.Error())
					continue
				}
				recoveryMap, err := store.GetUsersByIDs(collectUserIDs(recoveryAccesses))
				if err != nil {
					result.Errors = append(result.Errors, node.ID+": recovery load usermap: "+err.Error())
					continue
				}
				status, _, err := ApplyNodeUsers(ctx, client, nodeInbounds, recoveryAccesses, recoveryMap, ibStore, outboundStore, applyOpts, node)
				if err != nil {
					result.Errors = append(result.Errors, node.ID+": recovery restart: "+err.Error())
				} else if status.Running {
					result.NodesReloaded++
				} else {
					result.NodesStopped++
				}
			}
			continue
		}
		result.NodesSynced++

		// 每个 (user, node) 只有一条凭据记录，直接用于流量同步
		userAccesses, err := store.ListUserInboundsByNode(node.ID)
		if err != nil {
			result.Errors = append(result.Errors, node.ID+": "+err.Error())
			continue
		}

		// 批量获取涉及的 User
		userIDs := collectUserIDs(userAccesses)
		userMap, err := store.GetUsersByIDs(userIDs)
		if err != nil {
			result.Errors = append(result.Errors, node.ID+": "+err.Error())
			continue
		}

		usageByUser := make(map[string]nodes.UserUsage, len(usage.Users))
		for _, item := range usage.Users {
			usageByUser[item.User] = item
		}

		reloadNeeded := false

		// 节点维度流量从所有上报用户汇总（包含已删除用户），避免 reset=true 后丢失流量
		var nodeUploadDelta, nodeDownloadDelta int64
		for _, item := range usage.Users {
			nodeUploadDelta += item.UploadTotal
			nodeDownloadDelta += item.DownloadTotal
		}

		// 取节点倍率，≤0 时视为 1.0
		trafficRate := node.TrafficRate
		if trafficRate <= 0 {
			trafficRate = 1.0
		}

		// 按 UserID 去重，避免同一用户在同一节点有多条 inbound 时流量被重复计算。
		seenUsers := make(map[string]struct{})
		for _, acc := range userAccesses {
			if _, seen := seenUsers[acc.UserID]; seen {
				continue
			}
			seenUsers[acc.UserID] = struct{}{}

			user, ok := userMap[acc.UserID]
			if !ok {
				continue
			}
			prevEnabled := user.EffectiveEnabledAt(now)

			// 本轮首次处理该用户时，清零连接数和设备数，确保跨节点累加从零开始
			if _, seen := connResetUsers[user.ID]; !seen {
				user.Connections = 0
				user.Devices = 0
				connResetUsers[user.ID] = struct{}{}
			}

			if stats, ok := usageByUser[user.Username]; ok {
				uploadDelta := stats.UploadTotal
				downloadDelta := stats.DownloadTotal
				// 用户计费流量乘以节点倍率，同时累加实际原始流量
				user.UploadBytes += applyRate(uploadDelta, trafficRate)
				user.DownloadBytes += applyRate(downloadDelta, trafficRate)
				user.RawUploadBytes += uploadDelta
				user.RawDownloadBytes += downloadDelta
				// 累加连接数和在线设备数（支持多节点求和）
				user.Connections += stats.Connections
				user.Devices += stats.Devices
				// 有新增流量则更新在线时间
				if uploadDelta > 0 || downloadDelta > 0 {
					user.OnlineAt = &now
				}
			}

			user.UsedBytes = user.UploadBytes + user.DownloadBytes
			statusChanged := prevEnabled != user.EffectiveEnabledAt(now)
			user, err = store.UpsertUser(user)
			if err != nil {
				result.Errors = append(result.Errors, node.ID+": "+err.Error())
				continue
			}
			// 仅在用户状态持久化成功后才触发节点重下发，避免基于未保存状态重载
			if statusChanged {
				reloadNeeded = true
				// 状态变为 limited/expired 时发送一次性告警（状态已持久化，不会重复触发）
				switch user.EffectiveStatusAt(now) {
				case users.StatusLimited:
					sendAlert(ctx, applyOpts.Alerter, "流量超限", fmt.Sprintf("用户 %s 已超出流量限额", user.Username))
				case users.StatusExpired:
					sendAlert(ctx, applyOpts.Alerter, "用户到期", fmt.Sprintf("用户 %s 已到期", user.Username))
				}
			}
			result.UsersUpdated++
			userMap[user.ID] = user
		}

		// 累积节点维度流量（使用从全部上报用户汇总的真实值）
		date := now.Format("2006-01-02")
		if nodeUploadDelta > 0 || nodeDownloadDelta > 0 {
			if err := nodeStore.AddTraffic(node.ID, nodeUploadDelta, nodeDownloadDelta); err != nil {
				result.Errors = append(result.Errors, node.ID+": add traffic: "+err.Error())
			}
			// 写入节点日统计桶（用于历史趋势图）
			if err := nodeStore.AddNodeDailyUsage(node.ID, date, nodeUploadDelta, nodeDownloadDelta); err != nil {
				result.Errors = append(result.Errors, node.ID+": daily usage: "+err.Error())
			}
		}

		// 写入用户-节点日流量（用于节点用量分析）
		for userID, user := range userMap {
			stats, ok := usageByUser[user.Username]
			if !ok || (stats.UploadTotal == 0 && stats.DownloadTotal == 0) {
				continue
			}
			upload := applyRate(stats.UploadTotal, trafficRate)
			download := applyRate(stats.DownloadTotal, trafficRate)
			if err := store.AddUserNodeTraffic(userID, node.ID, date, upload, download); err != nil {
				result.Errors = append(result.Errors, node.ID+": user node traffic: "+err.Error())
			}
		}

		if !reloadNeeded {
			continue
		}

		// 获取节点 inbound 配置，重新下发
		nodeInbounds, err := ibStore.ListInboundsByNode(node.ID)
		if err != nil {
			result.Errors = append(result.Errors, node.ID+": reload inbounds: "+err.Error())
			continue
		}

		// 重新查全量 userMap（流量已更新）
		allUserIDs := collectUserIDs(userAccesses)
		allUserMap, err := store.GetUsersByIDs(allUserIDs)
		if err != nil {
			result.Errors = append(result.Errors, node.ID+": reload usermap: "+err.Error())
			continue
		}

		status, _, err := ApplyNodeUsers(ctx, client, nodeInbounds, userAccesses, allUserMap, ibStore, outboundStore, applyOpts, node)
		if err != nil {
			result.Errors = append(result.Errors, node.ID+": reload: "+err.Error())
			continue
		}
		if status.Running {
			result.NodesReloaded++
		} else {
			result.NodesStopped++
		}
	}

	// 定期清理过期的日统计数据（保留 180 天）
	if err := nodeStore.CleanupOldDailyUsage(180); err != nil {
		result.Errors = append(result.Errors, "cleanup daily usage: "+err.Error())
	}

	return result, nil
}

// ─── ActivateExpiredOnHold ────────────────────────────────────────────────────

// ActivateExpiredOnHold 将 on_hold_expire_at 已到期的 on_hold 用户状态改为 active，
// 并对涉及的节点重新下发配置。
func ActivateExpiredOnHold(ctx context.Context, store users.Store, nodeStore nodes.Store, ibStore inbounds.InboundStore, dial NodeDialer, applyOpts ApplyOptions, outboundStore outbounds.Store) error {
	allUsers, err := store.ListUsers()
	if err != nil {
		return err
	}

	now := time.Now().UTC()
	dirtyNodes := make(map[string]struct{})

	for _, u := range allUsers {
		if u.Status != users.StatusOnHold {
			continue
		}
		if u.OnHoldExpireAt == nil || u.OnHoldExpireAt.IsZero() || now.Before(*u.OnHoldExpireAt) {
			continue
		}
		u.Status = users.StatusActive
		u.OnHoldExpireAt = nil
		if _, err := store.UpsertUser(u); err != nil {
			log.Printf("ActivateExpiredOnHold: 激活用户 %s (%s) 失败: %v", u.Username, u.ID, err)
			continue
		}
		log.Printf("ActivateExpiredOnHold: 用户 %s (%s) 已激活", u.Username, u.ID)
		accesses, _ := store.ListUserInboundsByUser(u.ID)
		for _, acc := range accesses {
			dirtyNodes[acc.NodeID] = struct{}{}
		}
	}

	for nodeID := range dirtyNodes {
		client, err := dial(nodeID)
		if err != nil {
			continue
		}
		nodeInbounds, err := ibStore.ListInboundsByNode(nodeID)
		if err != nil {
			continue
		}
		nodeAccesses, err := store.ListUserInboundsByNode(nodeID)
		if err != nil {
			continue
		}
		userIDs := collectUserIDs(nodeAccesses)
		userMap, err := store.GetUsersByIDs(userIDs)
		if err != nil {
			continue
		}
		node, _ := nodeStore.Get(nodeID)
		ApplyNodeUsers(ctx, client, nodeInbounds, nodeAccesses, userMap, ibStore, outboundStore, applyOpts, node) //nolint:errcheck
	}

	return nil
}

// ─── ResetTraffic ─────────────────────────────────────────────────────────────

// ResetTrafficResult 记录一次流量重置的结果摘要。
type ResetTrafficResult struct {
	UsersReset    int      `json:"users_reset"`
	NodesReloaded int      `json:"nodes_reloaded"`
	Errors        []string `json:"errors"`
}

// ResetTraffic 检查所有用户的流量重置策略，到期则清零并重新下发节点配置。
func ResetTraffic(ctx context.Context, store users.Store, nodeStore nodes.Store, ibStore inbounds.InboundStore, dial NodeDialer, applyOpts ApplyOptions, outboundStore outbounds.Store) (ResetTrafficResult, error) {
	allUsers, err := store.ListUsers()
	if err != nil {
		return ResetTrafficResult{}, err
	}

	result := ResetTrafficResult{Errors: make([]string, 0)}
	now := time.Now().UTC()

	dirtyNodes := make(map[string]struct{})

	for _, user := range allUsers {
		if !ShouldResetTraffic(user.DataLimitResetStrategy, user.CreatedAt, user.LastTrafficResetAt, now) {
			continue
		}
		user.UploadBytes = 0
		user.DownloadBytes = 0
		user.UsedBytes = 0
		user.RawUploadBytes = 0
		user.RawDownloadBytes = 0
		user.LastTrafficResetAt = &now
		if _, err := store.UpsertUser(user); err != nil {
			result.Errors = append(result.Errors, user.ID+": "+err.Error())
			continue
		}

		// 标记涉及的节点为 dirty，需要重新下发配置
		userAccesses, err := store.ListUserInboundsByUser(user.ID)
		if err != nil {
			result.Errors = append(result.Errors, user.ID+": list accesses: "+err.Error())
			continue
		}
		for _, acc := range userAccesses {
			dirtyNodes[acc.NodeID] = struct{}{}
		}

		result.UsersReset++
	}

	if len(dirtyNodes) == 0 {
		return result, nil
	}

	// 对涉及的节点重新下发配置
	for nodeID := range dirtyNodes {
		client, err := dial(nodeID)
		if err != nil {
			result.Errors = append(result.Errors, nodeID+": "+err.Error())
			continue
		}
		nodeInbounds, err := ibStore.ListInboundsByNode(nodeID)
		if err != nil {
			result.Errors = append(result.Errors, nodeID+": list inbounds: "+err.Error())
			continue
		}
		nodeAccesses, err := store.ListUserInboundsByNode(nodeID)
		if err != nil {
			result.Errors = append(result.Errors, nodeID+": list accesses: "+err.Error())
			continue
		}
		userIDs := collectUserIDs(nodeAccesses)
		userMap, err := store.GetUsersByIDs(userIDs)
		if err != nil {
			result.Errors = append(result.Errors, nodeID+": get users: "+err.Error())
			continue
		}
		node, _ := nodeStore.Get(nodeID)
		status, _, err := ApplyNodeUsers(ctx, client, nodeInbounds, nodeAccesses, userMap, ibStore, outboundStore, applyOpts, node)
		if err != nil {
			result.Errors = append(result.Errors, nodeID+": reload: "+err.Error())
			continue
		}
		if status.Running {
			result.NodesReloaded++
		}
	}

	return result, nil
}

// ─── ShouldResetTraffic ───────────────────────────────────────────────────────

// ShouldResetTraffic 判断是否应按策略重置用户流量（纯函数，便于测试）。
func ShouldResetTraffic(strategy string, createdAt time.Time, lastResetAt *time.Time, now time.Time) bool {
	if strategy == users.ResetStrategyNoReset || strategy == "" {
		return false
	}

	ref := createdAt
	if lastResetAt != nil && !lastResetAt.IsZero() {
		ref = *lastResetAt
	}

	var next time.Time
	switch strategy {
	case users.ResetStrategyDay:
		next = ref.Add(24 * time.Hour)
	case users.ResetStrategyWeek:
		next = ref.AddDate(0, 0, 7)
	case users.ResetStrategyMonth:
		next = ref.AddDate(0, 1, 0)
	case users.ResetStrategyYear:
		next = ref.AddDate(1, 0, 0)
	default:
		return false
	}

	return !now.Before(next)
}

// ─── ApplyNode ────────────────────────────────────────────────────────────────

// ApplyNode 根据节点 ID 从各 Store 汇集数据后调用 ApplyNodeUsers，
// 适合在 inbound 变更（新增/修改/删除）后立即调用。
func ApplyNode(ctx context.Context, nodeID string, nodeStore nodes.Store, userStore users.Store, ibStore inbounds.InboundStore, outboundStore outbounds.Store, dial NodeDialer, opts ApplyOptions) error {
	node, err := nodeStore.Get(nodeID)
	if err != nil {
		return err
	}
	client, err := dial(nodeID)
	if err != nil {
		return err
	}
	nodeInbounds, err := ibStore.ListInboundsByNode(nodeID)
	if err != nil {
		return err
	}
	userAccesses, err := userStore.ListUserInboundsByNode(nodeID)
	if err != nil {
		return err
	}
	userMap, err := userStore.GetUsersByIDs(collectUserIDs(userAccesses))
	if err != nil {
		return err
	}
	_, _, err = ApplyNodeUsers(ctx, client, nodeInbounds, userAccesses, userMap, ibStore, outboundStore, opts, node)
	return err
}

// ─── ApplyNodeUsers ───────────────────────────────────────────────────────────

// Alerter 告警发送接口，nil 表示不发送。
type Alerter interface {
	Send(ctx context.Context, title, body string) error
}

// ApplyOptions 控制 ApplyNodeUsers 的行为。
type ApplyOptions struct {
	Alerter        Alerter          // nil 时不发送告警
	RouteRuleStore routerules.Store // nil 时不应用全局分流规则
}

// ApplyNodeUsers 根据节点 inbound 配置和用户凭据生成配置并下发到节点。
// nodeInbounds 是节点 inbound 定义，userAccesses 是用户凭据列表（每用户一条）。
func ApplyNodeUsers(ctx context.Context, client *nodes.Client, nodeInbounds []inbounds.Inbound, userAccesses []users.UserInbound, userMap map[string]users.User, ibStore inbounds.InboundStore, outboundStore outbounds.Store, applyOpts ApplyOptions, node nodes.Node) (nodes.Status, string, error) {
	// 过滤出已启用用户
	activeAccesses := filterEnabled(userAccesses, userMap)
	if len(activeAccesses) == 0 || len(nodeInbounds) == 0 {
		// 没有活跃用户或 Inbound 时，用最小配置保持 sing-box 进程存活
		idleCfg := proxycfg.BuildIdleConfig()
		status, err := client.Restart(ctx, nodes.ConfigRequest{Config: idleCfg})
		if err == nil {
			if syncErr := client.SyncCaddyRoutes(ctx, nil); syncErr != nil {
				log.Printf("warn: caddy sync (idle): %v", syncErr)
			}
		}
		return status, idleCfg, err
	}

	// 加载出口 map
	outboundMap := make(map[string]outbounds.Outbound)
	if outboundStore != nil {
		list, _ := outboundStore.List()
		for _, ob := range list {
			outboundMap[ob.ID] = ob
		}
	}

	// 加载全局分流规则
	var globalRouteRules []routerules.RouteRule
	if applyOpts.RouteRuleStore != nil {
		globalRouteRules, _ = applyOpts.RouteRuleStore.List()
	}

	cfg, err := proxycfg.BuildSingboxConfig(nodeInbounds, userAccesses, userMap, proxycfg.BuildOptions{
		OutboundMap: outboundMap,
		RouteRules:  globalRouteRules,
		NodeID:      node.ID,
	})
	if err != nil {
		return nodes.Status{}, "", err
	}

	status, err := client.Restart(ctx, nodes.ConfigRequest{Config: cfg})
	if err != nil {
		return status, cfg, err
	}

	// 同步当前生效的 Trojan 路由到 Caddy（节点无 Caddy 时 SyncCaddyRoutes 静默跳过）
	if ibStore != nil {
		routes := collectTrojanRoutes(nodeInbounds, ibStore)
		if syncErr := client.SyncCaddyRoutes(ctx, routes); syncErr != nil {
			log.Printf("warn: caddy sync: %v", syncErr)
		}
	}

	return status, cfg, nil
}

// collectTrojanRoutes 从节点 inbound 的 host 模板中提取 Trojan 路由（域名+端口，去重）。
func collectTrojanRoutes(nodeInbounds []inbounds.Inbound, ibStore inbounds.InboundStore) []nodes.TrojanRoute {
	seen := make(map[string]struct{})
	out := make([]nodes.TrojanRoute, 0)
	for _, ib := range nodeInbounds {
		if ib.Protocol != "trojan" {
			continue
		}
		hosts, err := ibStore.ListHostsByInbound(ib.ID)
		if err != nil {
			continue
		}
		for _, h := range hosts {
			if h.Address != "" {
				if _, ok := seen[h.Address]; !ok {
					seen[h.Address] = struct{}{}
					out = append(out, nodes.TrojanRoute{Domain: h.Address, Port: ib.Port})
				}
			}
		}
	}
	return out
}

func filterEnabled(accesses []users.UserInbound, userMap map[string]users.User) []users.UserInbound {
	out := make([]users.UserInbound, 0, len(accesses))
	for _, acc := range accesses {
		u, ok := userMap[acc.UserID]
		if ok && u.EffectiveEnabled() {
			out = append(out, acc)
		}
	}
	return out
}

// collectUserIDs 从 userAccesses 列表中提取去重后的 UserID。
func collectUserIDs(accesses []users.UserInbound) []string {
	seen := make(map[string]struct{})
	out := make([]string, 0)
	for _, acc := range accesses {
		if _, ok := seen[acc.UserID]; !ok {
			seen[acc.UserID] = struct{}{}
			out = append(out, acc.UserID)
		}
	}
	return out
}

// sendAlert 在后台 goroutine 中发送告警，不阻塞主流程。a 为 nil 时静默跳过。
func sendAlert(ctx context.Context, a Alerter, title, body string) {
	if a == nil {
		return
	}
	go func() {
		alertCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := a.Send(alertCtx, title, body); err != nil {
			log.Printf("alert send error: %v", err)
		}
	}()
}

// applyRate 将 delta 乘以倍率并防止 int64 溢出。
func applyRate(delta int64, rate float64) int64 {
	// float64(1<<63 - 1) 在 float64 中向上取整为 2^63 = 9.223372036854776e+18，
	// 任何 >= 该值的 float64 转换为 int64 都会溢出，因此用它作为上界。
	const maxInt64Float = float64(1<<63 - 1)
	scaled := float64(delta) * rate
	if scaled >= maxInt64Float {
		return 1<<63 - 1 // math.MaxInt64
	}
	return int64(scaled)
}
