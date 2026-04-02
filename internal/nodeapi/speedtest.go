package nodeapi

import (
	"bytes"
	"context"
	"crypto/rand"
	"io"
	"net/http"
	"time"
)

const speedTestBytes = 10 * 1024 * 1024 // 10 MB

type speedTestResult struct {
	DownBps int64 `json:"down_bps"`
	UpBps   int64 `json:"up_bps"`
}

func (a *API) handleSpeedTest(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeMethodNotAllowed(w, http.MethodGet)
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 60*time.Second)
	defer cancel()

	downBps, downErr := measureDownload(ctx)
	upBps, _ := measureUpload(ctx)

	if downErr != nil && upBps == 0 {
		writeJSON(w, http.StatusBadGateway, map[string]any{"error": downErr.Error()})
		return
	}

	writeJSON(w, http.StatusOK, speedTestResult{
		DownBps: downBps,
		UpBps:   upBps,
	})
}

func measureDownload(ctx context.Context) (int64, error) {
	client := &http.Client{Timeout: 30 * time.Second}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet,
		"https://speed.cloudflare.com/__down?bytes=10485760", nil)
	if err != nil {
		return 0, err
	}
	req.Header.Set("User-Agent", checkUserAgent)

	start := time.Now()
	resp, err := client.Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()

	n, err := io.Copy(io.Discard, resp.Body)
	if err != nil {
		return 0, err
	}

	elapsed := time.Since(start).Seconds()
	if elapsed == 0 {
		return 0, nil
	}
	return int64(float64(n) / elapsed), nil
}

func measureUpload(ctx context.Context) (int64, error) {
	// 内存生成随机数据，不写磁盘
	data := make([]byte, speedTestBytes)
	if _, err := rand.Read(data); err != nil {
		return 0, err
	}

	client := &http.Client{Timeout: 30 * time.Second}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		"https://speed.cloudflare.com/__up", bytes.NewReader(data))
	if err != nil {
		return 0, err
	}
	req.Header.Set("Content-Type", "application/octet-stream")
	req.Header.Set("User-Agent", checkUserAgent)
	req.ContentLength = speedTestBytes

	start := time.Now()
	resp, err := client.Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	io.Copy(io.Discard, resp.Body)

	elapsed := time.Since(start).Seconds()
	if elapsed == 0 {
		return 0, nil
	}
	return int64(float64(speedTestBytes) / elapsed), nil
}
