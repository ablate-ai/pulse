//go:build js && wasm

package main

import (
	"fmt"
	"strconv"
	"strings"
)

func escape(value string) string {
	replacer := strings.NewReplacer(
		"&", "&amp;",
		"<", "&lt;",
		">", "&gt;",
		`"`, "&quot;",
		"'", "&#39;",
	)
	return replacer.Replace(value)
}

func displayTime(value string) string {
	if strings.TrimSpace(value) == "" || value == "0001-01-01T00:00:00Z" {
		return "未下发"
	}
	return value
}

func parseInt64(value string) int64 {
	value = strings.TrimSpace(value)
	if value == "" {
		return 0
	}
	parsed, err := strconv.ParseInt(value, 10, 64)
	if err != nil {
		return 0
	}
	return parsed
}

func formatLimit(value int64) string {
	if value <= 0 {
		return "不限"
	}
	return fmt.Sprintf("%dB", value)
}

func userStatus(u user) string {
	if !u.Enabled {
		return "已停用"
	}
	return "启用中"
}
