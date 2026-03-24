package serverapi

import (
	"context"
	"net/http"
	"time"

	"pulse/internal/cert"
	"pulse/internal/config"
	"pulse/internal/nodes"
	"pulse/internal/users"
)

type systemAPI struct {
	users             users.Store
	nodes             nodes.Store
	base              *API
	nodeClientCertPEM string
}

func RegisterSystemAPI(mux *http.ServeMux, usersStore users.Store, nodesStore nodes.Store, clientOptions nodes.ClientOptions) {
	cfg := config.Load()
	clientCertPEM, _ := cert.ReadCertificatePEM(cfg.ServerNodeClientCertFile)
	base := New(nodesStore, clientOptions)
	api := &systemAPI{
		users:             usersStore,
		nodes:             nodesStore,
		base:              base,
		nodeClientCertPEM: clientCertPEM,
	}
	mux.HandleFunc("/v1/node/settings", api.handleNodeSettings)
	mux.HandleFunc("/v1/system/sync-usage", api.handleSyncUsage)
}

func (a *systemAPI) handleNodeSettings(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeMethodNotAllowed(w, http.MethodGet)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"certificate": a.nodeClientCertPEM,
	})
}

func (a *systemAPI) handleSyncUsage(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeMethodNotAllowed(w, http.MethodPost)
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()

	result, err := syncUsage(ctx, a.users, a.nodes, a.base)
	if err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]any{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, result)
}

type syncUsageResult struct {
	NodesSynced   int      `json:"nodes_synced"`
	UsersUpdated  int      `json:"users_updated"`
	NodesReloaded int      `json:"nodes_reloaded"`
	NodesStopped  int      `json:"nodes_stopped"`
	Errors        []string `json:"errors"`
}

func syncUsage(ctx context.Context, userStore users.Store, nodeStore nodes.Store, base *API) (syncUsageResult, error) {
	nodesList, err := nodeStore.List()
	if err != nil {
		return syncUsageResult{}, err
	}

	result := syncUsageResult{
		Errors: make([]string, 0),
	}

	for _, node := range nodesList {
		client, err := base.clientFor(node.ID)
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
			user.Enabled = user.TrafficLimit == 0 || user.UsedBytes < user.TrafficLimit
			nextEnabled := user.EffectiveEnabled()
			if prevEnabled != nextEnabled {
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

		status, _, err := applyNodeUsers(ctx, client, updatedUsers)
		if err != nil {
			result.Errors = append(result.Errors, node.ID+": reload failed: "+err.Error())
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

func usageDelta(current, previous int64) int64 {
	if current < previous {
		return current
	}
	return current - previous
}
