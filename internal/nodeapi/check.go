package nodeapi

import (
	"context"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"
)

// serviceCheckResult 单个服务的检测结果。
type serviceCheckResult struct {
	Service  string `json:"service"`
	Unlocked bool   `json:"unlocked"`
	Region   string `json:"region,omitempty"`
}

func (a *API) handleCheck(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeMethodNotAllowed(w, http.MethodGet)
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()

	type checker struct {
		fn func(context.Context) serviceCheckResult
	}
	checkers := []checker{
		{checkNetflix},
		{checkClaude},
		{checkOpenAI},
		{checkDisney},
	}

	results := make([]serviceCheckResult, len(checkers))
	var wg sync.WaitGroup
	for i, c := range checkers {
		wg.Add(1)
		go func(idx int, fn func(context.Context) serviceCheckResult) {
			defer wg.Done()
			results[idx] = fn(ctx)
		}(i, c.fn)
	}
	wg.Wait()

	writeJSON(w, http.StatusOK, map[string]any{"results": results})
}

const checkUserAgent = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.0.0 Safari/537.36"

func newCheckClient() *http.Client {
	return &http.Client{
		Timeout: 10 * time.Second,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= 3 {
				return http.ErrUseLastResponse
			}
			return nil
		},
	}
}

func doCheck(ctx context.Context, rawURL string) (*http.Response, error) {
	client := newCheckClient()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", checkUserAgent)
	return client.Do(req)
}

func checkNetflix(ctx context.Context) serviceCheckResult {
	r := serviceCheckResult{Service: "netflix"}
	resp, err := doCheck(ctx, "https://www.netflix.com/title/70143836")
	if err != nil {
		return r
	}
	defer resp.Body.Close()
	io.Copy(io.Discard, resp.Body)

	finalURL := resp.Request.URL.String()
	if resp.StatusCode == 200 && !strings.Contains(finalURL, "unavailable") {
		r.Unlocked = true
		// 从重定向后的 URL 路径中提取地区代码，如 /hk-zh/ -> "HK"
		for _, seg := range strings.Split(finalURL, "/") {
			if len(seg) == 5 && seg[2] == '-' {
				r.Region = strings.ToUpper(seg[:2])
				break
			}
		}
	}
	return r
}

func checkClaude(ctx context.Context) serviceCheckResult {
	r := serviceCheckResult{Service: "claude"}
	resp, err := doCheck(ctx, "https://claude.ai")
	if err != nil {
		return r
	}
	defer resp.Body.Close()
	io.Copy(io.Discard, resp.Body)
	r.Unlocked = resp.StatusCode < 400
	return r
}

func checkOpenAI(ctx context.Context) serviceCheckResult {
	r := serviceCheckResult{Service: "openai"}
	resp, err := doCheck(ctx, "https://api.openai.com")
	if err != nil {
		return r
	}
	defer resp.Body.Close()
	io.Copy(io.Discard, resp.Body)
	r.Unlocked = resp.StatusCode < 400
	return r
}

func checkDisney(ctx context.Context) serviceCheckResult {
	r := serviceCheckResult{Service: "disney+"}
	resp, err := doCheck(ctx, "https://www.disneyplus.com")
	if err != nil {
		return r
	}
	defer resp.Body.Close()
	io.Copy(io.Discard, resp.Body)
	// 200 = 可访问；403/451 = 地区限制
	r.Unlocked = resp.StatusCode == 200 || resp.StatusCode == 302
	return r
}
