package serverapi

import (
	"encoding/base64"
	"fmt"
	"log"
	"net"
	"net/http"
	"strings"

	"pulse/internal/inbounds"
	"pulse/internal/subscription"
	"pulse/internal/users"
)

type subAPI struct {
	users    users.Store
	inbounds inbounds.InboundStore
}

// RegisterSubAPI 注册公开订阅端点 GET /sub/{userID}，无需认证。
func RegisterSubAPI(mux *http.ServeMux, userStore users.Store, ibStore inbounds.InboundStore) {
	a := &subAPI{users: userStore, inbounds: ibStore}
	mux.HandleFunc("/sub/", a.handleSub)
}

func (a *subAPI) handleSub(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.NotFound(w, r)
		return
	}

	// 从路径提取 token：/sub/{token}
	userID := strings.TrimPrefix(r.URL.Path, "/sub/")
	userID = strings.TrimSuffix(userID, "/")
	if userID == "" {
		http.NotFound(w, r)
		return
	}

	user, err := a.users.GetUserBySubToken(userID)
	if err != nil {
		http.NotFound(w, r)
		return
	}

	// 异步记录订阅访问日志
	go func() {
		ip := realIP(r)
		ua := r.Header.Get("User-Agent")
		if err := a.users.LogSubAccess(user.ID, ip, ua); err != nil {
			log.Printf("sub access log: %v", err)
		}
	}()

	// 收集该用户所有节点的全部订阅链接
	accesses, err := a.users.ListUserInboundsByUser(user.ID)
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	var links []string
	for _, acc := range accesses {
		// 旧版记录（InboundID 为空）回退到节点级别获取所有 inbound
		if acc.InboundID == "" {
			nodeInbounds, err := a.inbounds.ListInboundsByNode(acc.NodeID)
			if err != nil {
				continue
			}
			for _, ib := range nodeInbounds {
				hosts, err := a.inbounds.ListHostsByInbound(ib.ID)
				if err != nil {
					continue
				}
				for _, h := range hosts {
					link := subscription.Link(ib, h, acc, user)
					if link != "" {
						links = append(links, link)
					}
				}
			}
			continue
		}
		ib, err := a.inbounds.GetInbound(acc.InboundID)
		if err != nil {
			continue
		}
		hosts, err := a.inbounds.ListHostsByInbound(ib.ID)
		if err != nil {
			continue
		}
		for _, h := range hosts {
			link := subscription.Link(ib, h, acc, user)
			if link != "" {
				links = append(links, link)
			}
		}
	}

	// Subscription-Userinfo header（客户端如 v2rayN 用于显示流量信息）
	w.Header().Set("Subscription-Userinfo", buildUserinfo(user))
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Header().Set("Profile-Update-Interval", "12") // 建议客户端每 12 小时更新

	// base64 编码，换行分隔（标准订阅格式）
	body := base64.StdEncoding.EncodeToString([]byte(strings.Join(links, "\n")))
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(body))
}

// realIP 从请求中提取客户端真实 IP（优先读 X-Real-IP / X-Forwarded-For）。
func realIP(r *http.Request) string {
	if ip := r.Header.Get("X-Real-IP"); ip != "" {
		return ip
	}
	if fwd := r.Header.Get("X-Forwarded-For"); fwd != "" {
		if idx := strings.IndexByte(fwd, ','); idx >= 0 {
			fwd = fwd[:idx]
		}
		return strings.TrimSpace(fwd)
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

// buildUserinfo 生成 Subscription-Userinfo header 值。
func buildUserinfo(u users.User) string {
	parts := []string{
		fmt.Sprintf("upload=%d", u.UploadBytes),
		fmt.Sprintf("download=%d", u.DownloadBytes),
	}
	if u.TrafficLimit > 0 {
		parts = append(parts, fmt.Sprintf("total=%d", u.TrafficLimit))
	}
	if u.ExpireAt != nil && !u.ExpireAt.IsZero() {
		parts = append(parts, fmt.Sprintf("expire=%d", u.ExpireAt.Unix()))
	}
	return strings.Join(parts, "; ")
}
