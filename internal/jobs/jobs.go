package jobs

import (
	"context"
	"log"
	"time"

	"pulse/internal/inbounds"
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
func SyncUsage(ctx context.Context, store users.Store, nodeStore nodes.Store, ibStore inbounds.InboundStore, dial NodeDialer, applyOpts ApplyOptions) (SyncUsageResult, error) {
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
		var nodeUploadDelta, nodeDownloadDelta int64

		for _, acc := range userAccesses {
			user, ok := userMap[acc.UserID]
			if !ok {
				continue
			}
			prevEnabled := user.EffectiveEnabled()

			if stats, ok := usageByUser[user.Username]; ok {
				uploadDelta := usageDelta(stats.UploadTotal, acc.SyncedUploadBytes)
				downloadDelta := usageDelta(stats.DownloadTotal, acc.SyncedDownloadBytes)
				user.UploadBytes += uploadDelta
				user.DownloadBytes += downloadDelta
				nodeUploadDelta += uploadDelta
				nodeDownloadDelta += downloadDelta
				acc.SyncedUploadBytes = stats.UploadTotal
				acc.SyncedDownloadBytes = stats.DownloadTotal
				// 有新增流量则更新在线时间
				if uploadDelta > 0 || downloadDelta > 0 {
					now := time.Now().UTC()
					user.OnlineAt = &now
				}
				// 保存更新后的游标
				if _, err := store.UpsertUserInbound(acc); err != nil {
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

		// 累积节点维度流量
		if nodeUploadDelta > 0 || nodeDownloadDelta > 0 {
			if err := nodeStore.AddTraffic(node.ID, nodeUploadDelta, nodeDownloadDelta); err != nil {
				result.Errors = append(result.Errors, node.ID+": add traffic: "+err.Error())
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

		status, _, err := ApplyNodeUsers(ctx, client, nodeInbounds, userAccesses, allUserMap, ibStore, applyOpts, node.CaddyEnabled)
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

// ─── ActivateExpiredOnHold ────────────────────────────────────────────────────

// ActivateExpiredOnHold 将 on_hold_expire_at 已到期的 on_hold 用户状态改为 active，
// 并对涉及的节点重新下发配置。
func ActivateExpiredOnHold(ctx context.Context, store users.Store, nodeStore nodes.Store, ibStore inbounds.InboundStore, dial NodeDialer, applyOpts ApplyOptions) error {
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
			continue
		}
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
		ApplyNodeUsers(ctx, client, nodeInbounds, nodeAccesses, userMap, ibStore, applyOpts, node.CaddyEnabled) //nolint:errcheck
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
func ResetTraffic(ctx context.Context, store users.Store, nodeStore nodes.Store, ibStore inbounds.InboundStore, dial NodeDialer, applyOpts ApplyOptions) (ResetTrafficResult, error) {
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
		user.LastTrafficResetAt = &now
		if _, err := store.UpsertUser(user); err != nil {
			result.Errors = append(result.Errors, user.ID+": "+err.Error())
			continue
		}

		// 清空该用户所有 access 的流量同步游标
		userAccesses, err := store.ListUserInboundsByUser(user.ID)
		if err != nil {
			result.Errors = append(result.Errors, user.ID+": list accesses: "+err.Error())
			continue
		}
		for _, acc := range userAccesses {
			acc.SyncedUploadBytes = 0
			acc.SyncedDownloadBytes = 0
			if _, err := store.UpsertUserInbound(acc); err != nil {
				result.Errors = append(result.Errors, user.ID+": reset cursor: "+err.Error())
			}
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
		status, _, err := ApplyNodeUsers(ctx, client, nodeInbounds, nodeAccesses, userMap, ibStore, applyOpts, node.CaddyEnabled)
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

// ─── ApplyNodeUsers ───────────────────────────────────────────────────────────

// ApplyOptions 控制 ApplyNodeUsers 的行为。
type ApplyOptions struct {
}

// ApplyNodeUsers 根据节点 inbound 配置和用户凭据生成配置并下发到节点。
// nodeInbounds 是节点 inbound 定义，userAccesses 是用户凭据列表（每用户一条）。
// caddyEnabled 为 true 时，Trojan inbound 改为 127.0.0.1 监听并触发 Caddy 路由同步。
func ApplyNodeUsers(ctx context.Context, client *nodes.Client, nodeInbounds []inbounds.Inbound, userAccesses []users.UserInbound, userMap map[string]users.User, ibStore inbounds.InboundStore, applyOpts ApplyOptions, caddyEnabled bool) (nodes.Status, string, error) {
	// 过滤出已启用用户
	activeAccesses := filterEnabled(userAccesses, userMap)
	if len(activeAccesses) == 0 || len(nodeInbounds) == 0 {
		// 没有活跃用户或 Inbound 时，用最小配置保持 sing-box 进程存活
		idleCfg := proxycfg.BuildIdleConfig()
		status, err := client.Restart(ctx, nodes.ConfigRequest{Config: idleCfg})
		if err == nil && caddyEnabled {
			if syncErr := client.SyncCaddyRoutes(ctx, nil); syncErr != nil {
				log.Printf("warn: caddy sync (idle): %v", syncErr)
			}
		}
		return status, idleCfg, err
	}

	cfg, err := proxycfg.BuildSingboxConfig(nodeInbounds, userAccesses, userMap, proxycfg.BuildOptions{
		CaddyEnabled: caddyEnabled,
	})
	if err != nil {
		return nodes.Status{}, "", err
	}

	status, err := client.Restart(ctx, nodes.ConfigRequest{Config: cfg})
	if err != nil {
		return status, cfg, err
	}

	// Caddy 模式：同步当前生效的 Trojan 路由
	if caddyEnabled && ibStore != nil {
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

func usageDelta(current, previous int64) int64 {
	if current < previous {
		return current
	}
	return current - previous
}
