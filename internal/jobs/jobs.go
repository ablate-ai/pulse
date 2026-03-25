package jobs

import (
	"context"
	"log"
	"time"

	"pulse/internal/nodes"
	"pulse/internal/proxycfg"
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
func SyncUsage(ctx context.Context, store users.Store, nodeStore nodes.Store, dial NodeDialer, applyOpts ApplyOptions) (SyncUsageResult, error) {
	nodesList, err := nodeStore.List()
	if err != nil {
		return SyncUsageResult{}, err
	}

	result := SyncUsageResult{Errors: make([]string, 0)}

	for _, node := range nodesList {
		client, err := dial(node.ID)
		if err != nil {
			result.Errors = append(result.Errors, node.ID+": "+err.Error())
			continue
		}

		usage, err := client.Usage(ctx)
		if err != nil {
			result.Errors = append(result.Errors, node.ID+": "+err.Error())
			continue
		}
		result.NodesSynced++

		// 游标 inbound：每 (user, node) 只取 ID 最小的，用于流量去重
		cursorInbounds, err := store.ListCursorInboundsByNode(node.ID)
		if err != nil {
			result.Errors = append(result.Errors, node.ID+": "+err.Error())
			continue
		}

		// 全量 inbound：用于判断 reload 后下发
		allInbounds, err := store.ListUserInboundsByNode(node.ID)
		if err != nil {
			result.Errors = append(result.Errors, node.ID+": "+err.Error())
			continue
		}

		// 批量获取涉及的 User
		userIDs := collectUserIDs(cursorInbounds)
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

		for _, ib := range cursorInbounds {
			user, ok := userMap[ib.UserID]
			if !ok {
				continue
			}
			prevEnabled := user.EffectiveEnabled()

			if stats, ok := usageByUser[user.Username]; ok {
				user.UploadBytes += usageDelta(stats.UploadTotal, ib.SyncedUploadBytes)
				user.DownloadBytes += usageDelta(stats.DownloadTotal, ib.SyncedDownloadBytes)
				ib.SyncedUploadBytes = stats.UploadTotal
				ib.SyncedDownloadBytes = stats.DownloadTotal
				// 保存更新后的游标
				if _, err := store.UpsertUserInbound(ib); err != nil {
					result.Errors = append(result.Errors, node.ID+": "+err.Error())
					continue
				}
			}
			user.UsedBytes = user.UploadBytes + user.DownloadBytes
			if prevEnabled != user.EffectiveEnabled() {
				reloadNeeded = true
			}
			user, err = store.UpsertUser(user)
			if err != nil {
				result.Errors = append(result.Errors, node.ID+": "+err.Error())
				continue
			}
			result.UsersUpdated++
			userMap[user.ID] = user
		}

		if !reloadNeeded {
			continue
		}

		// 重新查全量 inbound 对应的 userMap（流量已更新）
		allUserIDs := collectUserIDs(allInbounds)
		allUserMap, err := store.GetUsersByIDs(allUserIDs)
		if err != nil {
			result.Errors = append(result.Errors, node.ID+": reload usermap: "+err.Error())
			continue
		}

		status, _, err := ApplyNodeUsers(ctx, client, allInbounds, allUserMap, applyOpts)
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

	return result, nil
}

// ─── ResetTraffic ─────────────────────────────────────────────────────────────

// ResetTrafficResult 记录一次流量重置的结果摘要。
type ResetTrafficResult struct {
	UsersReset    int      `json:"users_reset"`
	NodesReloaded int      `json:"nodes_reloaded"`
	Errors        []string `json:"errors"`
}

// ResetTraffic 检查所有用户的流量重置策略，到期则清零并重新下发节点配置。
func ResetTraffic(ctx context.Context, store users.Store, nodeStore nodes.Store, dial NodeDialer, applyOpts ApplyOptions) (ResetTrafficResult, error) {
	allUsers, err := store.ListUsers()
	if err != nil {
		return ResetTrafficResult{}, err
	}

	result := ResetTrafficResult{Errors: make([]string, 0)}
	now := time.Now().UTC()

	// 按节点分组需要重置的用户
	dirtyNodes := make(map[string]struct{})

	for _, user := range allUsers {
		if !ShouldResetTraffic(user.DataLimitResetStrategy, user.CreatedAt, user.LastTrafficResetAt, now) {
			continue
		}
		user.UploadBytes = 0
		user.DownloadBytes = 0
		user.UsedBytes = 0
		user.LastTrafficResetAt = &now
		if _, err := store.UpsertUser(user); err != nil {
			result.Errors = append(result.Errors, user.ID+": "+err.Error())
			continue
		}

		// 清空该用户所有 inbound 的游标
		userInbounds, err := store.ListUserInboundsByUser(user.ID)
		if err != nil {
			result.Errors = append(result.Errors, user.ID+": list inbounds: "+err.Error())
			continue
		}
		for _, ib := range userInbounds {
			ib.SyncedUploadBytes = 0
			ib.SyncedDownloadBytes = 0
			if _, err := store.UpsertUserInbound(ib); err != nil {
				result.Errors = append(result.Errors, user.ID+": reset inbound cursor: "+err.Error())
			}
			dirtyNodes[ib.NodeID] = struct{}{}
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
		nodeInbounds, err := store.ListUserInboundsByNode(nodeID)
		if err != nil {
			result.Errors = append(result.Errors, nodeID+": "+err.Error())
			continue
		}
		userIDs := collectUserIDs(nodeInbounds)
		userMap, err := store.GetUsersByIDs(userIDs)
		if err != nil {
			result.Errors = append(result.Errors, nodeID+": get users: "+err.Error())
			continue
		}
		status, _, err := ApplyNodeUsers(ctx, client, nodeInbounds, userMap, applyOpts)
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
// ref 为上次重置时间；若从未重置则使用 createdAt 作为参照点。
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

// ─── 内部工具 ─────────────────────────────────────────────────────────────────

// ApplyOptions 控制 ApplyNodeUsers 的行为。
type ApplyOptions struct {
	// SingboxWSLocalPort > 0 时，Trojan inbound 改为 WS 模式并监听该本地端口，
	// 由外部 Caddy 终止 TLS。0 = 直连模式。
	SingboxWSLocalPort int
}

// ApplyNodeUsers 根据节点入站配置列表和用户 map 生成配置并下发到节点。
// Caddy WS 模式下（SingboxWSLocalPort > 0）同步更新节点上的 Caddy Trojan 路由。
func ApplyNodeUsers(ctx context.Context, client *nodes.Client, nodeInbounds []users.UserInbound, userMap map[string]users.User, applyOpts ApplyOptions) (nodes.Status, string, error) {
	// 过滤出已启用用户的 inbound
	active := filterEnabled(nodeInbounds, userMap)
	if len(active) == 0 {
		status, err := client.Stop(ctx)
		if err == nil && applyOpts.SingboxWSLocalPort > 0 {
			// 清空该节点所有 Trojan Caddy 路由
			if syncErr := client.SyncCaddyRoutes(ctx, nil, applyOpts.SingboxWSLocalPort); syncErr != nil {
				log.Printf("warn: caddy sync (stop): %v", syncErr)
			}
		}
		return status, "", err
	}

	cfg, err := proxycfg.BuildSingboxConfig(active, userMap, proxycfg.BuildOptions{
		SingboxWSLocalPort: applyOpts.SingboxWSLocalPort,
	})
	if err != nil {
		return nodes.Status{}, "", err
	}

	status, err := client.Restart(ctx, nodes.ConfigRequest{Config: cfg})
	if err != nil {
		return status, cfg, err
	}

	// Caddy WS 模式：同步当前生效的 Trojan 域名路由
	if applyOpts.SingboxWSLocalPort > 0 {
		domains := collectTrojanDomains(active, userMap)
		if syncErr := client.SyncCaddyRoutes(ctx, domains, applyOpts.SingboxWSLocalPort); syncErr != nil {
			log.Printf("warn: caddy sync: %v", syncErr)
		}
	}

	return status, cfg, nil
}

// collectTrojanDomains 从入站列表中提取去重后的 Trojan 域名。
func collectTrojanDomains(inbounds []users.UserInbound, userMap map[string]users.User) []string {
	seen := make(map[string]struct{})
	out := make([]string, 0)
	for _, ib := range inbounds {
		u, ok := userMap[ib.UserID]
		if !ok || !u.EffectiveEnabled() {
			continue
		}
		if ib.Protocol == "trojan" && ib.Domain != "" {
			if _, ok := seen[ib.Domain]; !ok {
				seen[ib.Domain] = struct{}{}
				out = append(out, ib.Domain)
			}
		}
	}
	return out
}

func filterEnabled(inbounds []users.UserInbound, userMap map[string]users.User) []users.UserInbound {
	out := make([]users.UserInbound, 0, len(inbounds))
	for _, ib := range inbounds {
		u, ok := userMap[ib.UserID]
		if !ok {
			continue
		}
		if u.EffectiveEnabled() {
			out = append(out, ib)
		}
	}
	return out
}

// collectUserIDs 从入站列表中提取去重后的 UserID 列表。
func collectUserIDs(inbounds []users.UserInbound) []string {
	seen := make(map[string]struct{})
	out := make([]string, 0)
	for _, ib := range inbounds {
		if _, ok := seen[ib.UserID]; !ok {
			seen[ib.UserID] = struct{}{}
			out = append(out, ib.UserID)
		}
	}
	return out
}

func usageDelta(current, previous int64) int64 {
	if current < previous {
		return current
	}
	return current - previous
}
