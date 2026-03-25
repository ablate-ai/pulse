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
func SyncUsage(ctx context.Context, userStore users.Store, nodeStore nodes.Store, dial NodeDialer, applyOpts ApplyOptions) (SyncUsageResult, error) {
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

		nodeUsers, err := userStore.ListByNode(node.ID)
		if err != nil {
			result.Errors = append(result.Errors, node.ID+": "+err.Error())
			continue
		}

		usageByUser := make(map[string]nodes.UserUsage, len(usage.Users))
		for _, item := range usage.Users {
			usageByUser[item.User] = item
		}

		reloadNeeded := false
		updatedUsers := make([]users.User, 0, len(nodeUsers))
		for _, user := range nodeUsers {
			prevEnabled := user.EffectiveEnabled()
			if stats, ok := usageByUser[user.Username]; ok {
				user.UploadBytes += usageDelta(stats.UploadTotal, user.SyncedUploadBytes)
				user.DownloadBytes += usageDelta(stats.DownloadTotal, user.SyncedDownloadBytes)
				user.SyncedUploadBytes = stats.UploadTotal
				user.SyncedDownloadBytes = stats.DownloadTotal
			}
			user.UsedBytes = user.UploadBytes + user.DownloadBytes
			if prevEnabled != user.EffectiveEnabled() {
				reloadNeeded = true
			}
			user, err = userStore.Upsert(user)
			if err != nil {
				result.Errors = append(result.Errors, node.ID+": "+err.Error())
				continue
			}
			result.UsersUpdated++
			updatedUsers = append(updatedUsers, user)
		}

		if !reloadNeeded {
			continue
		}
		status, _, err := ApplyNodeUsers(ctx, client, updatedUsers, applyOpts)
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
func ResetTraffic(ctx context.Context, userStore users.Store, nodeStore nodes.Store, dial NodeDialer, applyOpts ApplyOptions) (ResetTrafficResult, error) {
	allUsers, err := userStore.List()
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
		user.SyncedUploadBytes = 0
		user.SyncedDownloadBytes = 0
		user.LastTrafficResetAt = &now
		if _, err := userStore.Upsert(user); err != nil {
			result.Errors = append(result.Errors, user.ID+": "+err.Error())
			continue
		}
		result.UsersReset++
		dirtyNodes[user.NodeID] = struct{}{}
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
		nodeUsers, err := userStore.ListByNode(nodeID)
		if err != nil {
			result.Errors = append(result.Errors, nodeID+": "+err.Error())
			continue
		}
		status, _, err := ApplyNodeUsers(ctx, client, nodeUsers, applyOpts)
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

// ApplyNodeUsers 根据节点用户列表生成配置并下发到节点。
// Caddy WS 模式下（SingboxWSLocalPort > 0）同步更新节点上的 Caddy Trojan 路由。
func ApplyNodeUsers(ctx context.Context, client *nodes.Client, nodeUsers []users.User, applyOpts ApplyOptions) (nodes.Status, string, error) {
	active := filterEnabled(nodeUsers)
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

	cfg, err := proxycfg.BuildSingboxConfig(active, proxycfg.BuildOptions{
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
		domains := collectTrojanDomains(active)
		if syncErr := client.SyncCaddyRoutes(ctx, domains, applyOpts.SingboxWSLocalPort); syncErr != nil {
			log.Printf("warn: caddy sync: %v", syncErr)
		}
	}

	return status, cfg, nil
}

// collectTrojanDomains 从用户列表中提取去重后的 Trojan 域名。
func collectTrojanDomains(userList []users.User) []string {
	seen := make(map[string]struct{})
	out := make([]string, 0)
	for _, u := range userList {
		if u.Protocol == "trojan" && u.Domain != "" {
			if _, ok := seen[u.Domain]; !ok {
				seen[u.Domain] = struct{}{}
				out = append(out, u.Domain)
			}
		}
	}
	return out
}

func filterEnabled(items []users.User) []users.User {
	out := make([]users.User, 0, len(items))
	for _, u := range items {
		if u.EffectiveEnabled() {
			out = append(out, u)
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
