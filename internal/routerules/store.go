package routerules

import "errors"

var ErrNotFound = errors.New("route rule not found")

// RouteRule 表示一条全局分流规则，优先于 inbound 绑定出口规则。
// RuleType 可选值：domain_suffix / domain_keyword / domain / ip_cidr
// Patterns 为逗号分隔的匹配列表（如 "openai.com,claude.ai"）。
// Priority 越小越优先；OutboundID 为空时流量走 direct。
type RouteRule struct {
	ID         string `json:"id"`
	Name       string `json:"name"`
	RuleType   string `json:"rule_type"`  // domain_suffix / domain_keyword / domain / ip_cidr
	Patterns   string `json:"patterns"`   // 逗号分隔
	OutboundID string `json:"outbound_id"` // 空 = direct
	Priority   int    `json:"priority"`
}

type Store interface {
	Upsert(rule RouteRule) (RouteRule, error)
	Get(id string) (RouteRule, error)
	List() ([]RouteRule, error)
	Delete(id string) error
}
