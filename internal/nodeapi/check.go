package nodeapi

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"sync"
	"time"
)

// serviceCheckResult 单个服务的检测结果。
type serviceCheckResult struct {
	Service  string `json:"service"`
	Unlocked bool   `json:"unlocked"`
	Region   string `json:"region,omitempty"`
	Note     string `json:"note,omitempty"`
}

func (a *API) handleCheck(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeMethodNotAllowed(w, http.MethodGet)
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()

	var (
		direct  []serviceCheckResult
		proxied []serviceCheckResult
		proxyAvailable bool
		wg      sync.WaitGroup
	)

	// 直连检测
	wg.Add(1)
	go func() {
		defer wg.Done()
		direct = runChecks(ctx, checkHTTPClient)
	}()

	// 代理检测（如果 sing-box 有 HTTP/SOCKS 入站）
	if proxyURL := findLocalProxyPort(a.manager.Config()); proxyURL != "" {
		proxyAvailable = true
		wg.Add(1)
		go func() {
			defer wg.Done()
			proxied = runChecks(ctx, newProxiedClient(proxyURL))
		}()
	}

	wg.Wait()

	resp := map[string]any{
		"direct":          direct,
		"proxy_available": proxyAvailable,
	}
	if proxyAvailable {
		resp["proxied"] = proxied
	}
	writeJSON(w, http.StatusOK, resp)
}

const checkUserAgent = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.0.0 Safari/537.36"

// checkHTTPClient 直连检测专用客户端。
var checkHTTPClient = &http.Client{
	Timeout: 9 * time.Second,
	CheckRedirect: func(req *http.Request, via []*http.Request) error {
		if len(via) >= 5 {
			return http.ErrUseLastResponse
		}
		return nil
	},
}

// newProxiedClient 创建经过本地代理的 HTTP 客户端。
func newProxiedClient(proxyURL string) *http.Client {
	u, _ := url.Parse(proxyURL)
	return &http.Client{
		Timeout: 9 * time.Second,
		Transport: &http.Transport{
			Proxy: http.ProxyURL(u),
		},
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= 5 {
				return http.ErrUseLastResponse
			}
			return nil
		},
	}
}

// findLocalProxyPort 从 sing-box 运行配置中找到第一个 HTTP/SOCKS/mixed 入站，
// 返回形如 "http://127.0.0.1:1080" 的代理 URL；找不到则返回空字符串。
func findLocalProxyPort(configJSON string) string {
	if configJSON == "" {
		return ""
	}
	var cfg struct {
		Inbounds []struct {
			Type       string `json:"type"`
			ListenPort int    `json:"listen_port"`
		} `json:"inbounds"`
	}
	if err := json.Unmarshal([]byte(configJSON), &cfg); err != nil {
		return ""
	}
	for _, ib := range cfg.Inbounds {
		switch ib.Type {
		case "mixed", "http":
			return fmt.Sprintf("http://127.0.0.1:%d", ib.ListenPort)
		case "socks":
			return fmt.Sprintf("socks5://127.0.0.1:%d", ib.ListenPort)
		}
	}
	return ""
}

type streamServiceDef struct {
	name    string
	url     string
	checkFn func(status int, finalURL, body string) (unlocked bool, region, note string)
}

// isoToName 将 ISO 3166-1 alpha-2 国家代码转为中文名，未收录则返回原代码。
func isoToName(code string) string {
	if code == "" {
		return ""
	}
	names := map[string]string{
		"US": "美国", "GB": "英国", "CA": "加拿大", "AU": "澳大利亚",
		"JP": "日本", "KR": "韩国", "SG": "新加坡", "HK": "香港",
		"TW": "台湾", "DE": "德国", "FR": "法国", "NL": "荷兰",
		"IT": "意大利", "ES": "西班牙", "PT": "葡萄牙", "SE": "瑞典",
		"NO": "挪威", "FI": "芬兰", "DK": "丹麦", "CH": "瑞士",
		"AT": "奥地利", "BE": "比利时", "PL": "波兰", "CZ": "捷克",
		"TR": "土耳其", "RU": "俄罗斯", "IN": "印度", "BR": "巴西",
		"MX": "墨西哥", "AR": "阿根廷", "CL": "智利", "CO": "哥伦比亚",
		"ZA": "南非", "TH": "泰国", "MY": "马来西亚", "ID": "印度尼西亚",
		"PH": "菲律宾", "VN": "越南", "NZ": "新西兰",
		"SA": "沙特阿拉伯", "AE": "阿联酋", "IL": "以色列",
	}
	if name, ok := names[strings.ToUpper(code)]; ok {
		return name
	}
	return strings.ToUpper(code)
}

// parseCountry 按优先级依次尝试 patterns，从响应体中提取 ISO 国家代码。
func parseCountry(body string, patterns []string) string {
	for _, p := range patterns {
		re := regexp.MustCompile(p)
		if m := re.FindStringSubmatch(body); len(m) > 1 {
			return strings.ToUpper(m[1])
		}
	}
	return ""
}

// streamServices 是所有待检测服务的定义列表。
var streamServices = []streamServiceDef{
	{
		name: "Netflix",
		url:  "https://www.netflix.com/",
		checkFn: func(status int, finalURL, body string) (bool, string, string) {
			if status != 200 {
				return false, "", "HTTP " + http.StatusText(status)
			}
			if strings.Contains(finalURL, "unavailable") || strings.Contains(body, "NotAvailable") {
				return false, "", "地区不可用"
			}
			code := parseCountry(body, []string{
				`"requestCountry"\s*:\s*\{\s*"id"\s*:\s*"([A-Z]{2})"`,
				`"countryCode"\s*:\s*"([A-Z]{2})"`,
				`"country"\s*:\s*"([A-Z]{2})"`,
			})
			return true, isoToName(code), ""
		},
	},
	{
		name: "YouTube",
		url:  "https://www.youtube.com/",
		checkFn: func(status int, finalURL, body string) (bool, string, string) {
			if status != 200 {
				return false, "", "HTTP " + http.StatusText(status)
			}
			code := parseCountry(body, []string{
				`"GL"\s*:\s*"([A-Z]{2})"`,
				`"gl"\s*:\s*"([a-zA-Z]{2})"`,
				`INNERTUBE_CONTEXT_GL[" ]*:\s*"([A-Z]{2})"`,
			})
			return true, isoToName(code), ""
		},
	},
	{
		name: "Disney+",
		url:  "https://www.disneyplus.com/",
		checkFn: func(status int, finalURL, body string) (bool, string, string) {
			if status == 200 {
				if strings.Contains(body, "not available") || strings.Contains(body, "coming soon") {
					return false, "", "地区不可用"
				}
				code := parseCountry(body, []string{
					`"countryCode"\s*:\s*"([A-Z]{2})"`,
					`"country"\s*:\s*"([A-Z]{2})"`,
					`"region"\s*:\s*"([A-Z]{2})"`,
				})
				return true, isoToName(code), ""
			}
			if status == 403 || status == 451 {
				return false, "", "地区封锁"
			}
			return false, "", "HTTP " + http.StatusText(status)
		},
	},
	{
		name: "Claude",
		url:  "https://claude.ai/api/auth/session",
		checkFn: func(status int, finalURL, body string) (bool, string, string) {
			if strings.Contains(finalURL, "unavailable") {
				return false, "", "地区不可用"
			}
			lower := strings.ToLower(body)
			if strings.Contains(lower, "unavailable") || strings.Contains(lower, "not available") {
				return false, "", "地区不可用"
			}
			if status >= 400 {
				return false, "", "HTTP " + http.StatusText(status)
			}
			return true, "", ""
		},
	},
	{
		name: "OpenAI",
		url:  "https://api.openai.com",
		checkFn: func(status int, finalURL, body string) (bool, string, string) {
			if status >= 400 {
				return false, "", "HTTP " + http.StatusText(status)
			}
			return true, "", ""
		},
	},
	{
		name: "Spotify",
		url:  "https://open.spotify.com/",
		checkFn: func(status int, finalURL, body string) (bool, string, string) {
			if status != 200 {
				return false, "", "HTTP " + http.StatusText(status)
			}
			code := parseCountry(body, []string{
				`"country"\s*:\s*"([A-Z]{2})"`,
				`"market"\s*:\s*"([A-Z]{2})"`,
			})
			return true, isoToName(code), ""
		},
	},
	{
		name: "TikTok",
		url:  "https://www.tiktok.com/",
		checkFn: func(status int, finalURL, body string) (bool, string, string) {
			return status == 200, "", ""
		},
	},
	{
		name: "Twitter/X",
		url:  "https://x.com/",
		checkFn: func(status int, finalURL, body string) (bool, string, string) {
			return status == 200, "", ""
		},
	},
	{
		name: "GitHub",
		url:  "https://github.com/",
		checkFn: func(status int, finalURL, body string) (bool, string, string) {
			return status == 200, "", ""
		},
	},
}

// runChecks 使用指定 client 并发检测所有服务，按定义顺序返回结果。
func runChecks(ctx context.Context, client *http.Client) []serviceCheckResult {
	results := make([]serviceCheckResult, len(streamServices))
	var wg sync.WaitGroup

	for i, svc := range streamServices {
		wg.Add(1)
		go func(idx int, s streamServiceDef) {
			defer wg.Done()
			result := serviceCheckResult{Service: s.name}

			req, err := http.NewRequestWithContext(ctx, http.MethodGet, s.url, nil)
			if err != nil {
				result.Note = "请求构建失败"
				results[idx] = result
				return
			}
			req.Header.Set("User-Agent", checkUserAgent)
			req.Header.Set("Accept-Language", "en-US,en;q=0.9")
			req.Header.Set("Accept", "text/html,application/xhtml+xml,application/xml;q=0.9,*/*;q=0.8")

			resp, err := client.Do(req)
			if err != nil {
				result.Note = "连接超时"
				results[idx] = result
				return
			}
			defer resp.Body.Close()

			body, _ := io.ReadAll(io.LimitReader(resp.Body, 8192))
			result.Unlocked, result.Region, result.Note = s.checkFn(
				resp.StatusCode, resp.Request.URL.String(), string(body),
			)
			results[idx] = result
		}(i, svc)
	}

	wg.Wait()
	return results
}
