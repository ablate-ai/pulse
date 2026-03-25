package tlsproxy

import (
	"context"
	"crypto/tls"
	"io"
	"log"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strings"
	"sync/atomic"

	"pulse/internal/certmgr"
)

// Route 将域名映射到后端地址（host:port）。
type Route struct {
	Host    string `json:"host"`
	Backend string `json:"backend"`
}

// Proxy 是基于 certmagic 的 TLS 反向代理。
// 支持动态路由更新，处理 HTTP 和 WebSocket 流量。
type Proxy struct {
	certs  *certmgr.Manager
	addr   string
	routes atomic.Pointer[[]Route]
}

// New 创建 Proxy，addr 为监听地址（如 ":443"）。
func New(certs *certmgr.Manager, addr string) *Proxy {
	p := &Proxy{certs: certs, addr: addr}
	empty := []Route{}
	p.routes.Store(&empty)
	return p
}

// SetRoutes 原子替换路由表。
func (p *Proxy) SetRoutes(routes []Route) {
	cp := make([]Route, len(routes))
	copy(cp, routes)
	p.routes.Store(&cp)
}

// Start 启动 TLS 监听并阻塞，ctx 取消时优雅退出。
func (p *Proxy) Start(ctx context.Context) error {
	tlsCfg := p.certs.TLSConfig()
	ln, err := tls.Listen("tcp", p.addr, tlsCfg)
	if err != nil {
		return err
	}
	srv := &http.Server{Handler: p}
	go func() {
		<-ctx.Done()
		_ = srv.Shutdown(context.Background())
	}()
	log.Printf("tls-proxy listening on %s", p.addr)
	if err := srv.Serve(ln); err != nil && err != http.ErrServerClosed {
		return err
	}
	return nil
}

func (p *Proxy) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	host := stripPort(r.Host)
	backend := ""
	for _, route := range *p.routes.Load() {
		if strings.EqualFold(stripPort(route.Host), host) {
			backend = route.Backend
			break
		}
	}
	if backend == "" {
		http.Error(w, "no route for host", http.StatusBadGateway)
		return
	}
	if strings.EqualFold(r.Header.Get("Upgrade"), "websocket") {
		tunnelWebSocket(w, r, backend)
		return
	}
	rp := httputil.NewSingleHostReverseProxy(&url.URL{Scheme: "http", Host: backend})
	rp.ServeHTTP(w, r)
}

func stripPort(host string) string {
	h, _, err := net.SplitHostPort(host)
	if err != nil {
		return host
	}
	return h
}

// tunnelWebSocket 在客户端和后端之间建立原始 TCP 隧道实现 WebSocket 代理。
func tunnelWebSocket(w http.ResponseWriter, r *http.Request, backend string) {
	conn, err := net.Dial("tcp", backend)
	if err != nil {
		http.Error(w, "backend unreachable", http.StatusBadGateway)
		return
	}
	defer conn.Close()

	hj, ok := w.(http.Hijacker)
	if !ok {
		http.Error(w, "hijack not supported", http.StatusInternalServerError)
		return
	}
	client, _, err := hj.Hijack()
	if err != nil {
		return
	}
	defer client.Close()

	// 将原始 HTTP 升级请求重放到后端
	if err := r.Write(conn); err != nil {
		return
	}
	done := make(chan struct{}, 1)
	go func() { _, _ = io.Copy(conn, client); done <- struct{}{} }()
	_, _ = io.Copy(client, conn)
	<-done
}
