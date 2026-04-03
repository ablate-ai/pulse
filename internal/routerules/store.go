package routerules

import "errors"

var ErrNotFound = errors.New("route rule not found")

// RouteRule 表示一条全局分流规则，优先于 inbound 绑定出口规则。
// RuleType 可选值：domain_suffix / domain_keyword / domain / ip_cidr / rule_set
// Patterns 为逗号分隔的匹配列表；rule_set 类型时为 sing-box tag 名称。
// Priority 越小越优先；OutboundID 为空时流量走 direct。
// RuleSetURL / RuleSetFormat 仅 rule_set 类型使用。
// NodeIDs 为逗号分隔的节点 ID 列表；空 = 下发到所有节点。
type RouteRule struct {
	ID            string `json:"id"`
	Name          string `json:"name"`
	RuleType      string `json:"rule_type"`       // domain_suffix / domain_keyword / domain / ip_cidr / rule_set
	Patterns      string `json:"patterns"`        // 逗号分隔；rule_set 时为 tag
	OutboundID    string `json:"outbound_id"`     // 空 = direct
	Priority      int    `json:"priority"`
	RuleSetURL    string `json:"rule_set_url,omitempty"`    // rule_set 类型的下载地址
	RuleSetFormat string `json:"rule_set_format,omitempty"` // "binary"（默认）或 "source"
	NodeIDs       string `json:"node_ids,omitempty"`        // 逗号分隔节点 ID；空 = 全部节点
}

type Store interface {
	Upsert(rule RouteRule) (RouteRule, error)
	Get(id string) (RouteRule, error)
	List() ([]RouteRule, error)
	Delete(id string) error
}
