package proxycfg

import (
	"encoding/json"
	"fmt"
	"sort"

	"pulse/internal/users"
)

type singboxConfig struct {
	Log       map[string]any   `json:"log"`
	Inbounds  []inbound        `json:"inbounds"`
	Outbounds []map[string]any `json:"outbounds"`
}

type inbound struct {
	Type       string           `json:"type"`
	Tag        string           `json:"tag"`
	Listen     string           `json:"listen"`
	ListenPort int              `json:"listen_port"`
	Users      []map[string]any `json:"users"`
	Transport  map[string]any   `json:"transport,omitempty"`
	Method     string           `json:"method,omitempty"`
	Password   string           `json:"password,omitempty"`
}

func BuildSingboxConfig(nodeUsers []users.User) (string, error) {
	if len(nodeUsers) == 0 {
		return "", fmt.Errorf("at least one user is required")
	}

	type inboundKey struct {
		Port     int
		Tag      string
		Protocol string
		Method   string
	}

	inboundIndex := make(map[inboundKey]int)
	inbounds := make([]inbound, 0)

	for _, user := range nodeUsers {
		key := inboundKey{
			Port:     user.Port,
			Tag:      inboundTag(user),
			Protocol: protocolOf(user),
			Method:   methodOf(user),
		}

		index, ok := inboundIndex[key]
		if !ok {
			inbounds = append(inbounds, inbound{
				Type:       key.Protocol,
				Tag:        key.Tag,
				Listen:     "::",
				ListenPort: user.Port,
				Users:      make([]map[string]any, 0, 1),
				Transport:  transportFor(key.Protocol),
				Method:     key.Method,
				Password:   inboundPasswordFor(key.Protocol, key.Method),
			})
			index = len(inbounds) - 1
			inboundIndex[key] = index
		}

		inbounds[index].Users = append(inbounds[index].Users, inboundUser(user)...)
	}

	sort.Slice(inbounds, func(i, j int) bool {
		if inbounds[i].ListenPort == inbounds[j].ListenPort {
			return inbounds[i].Tag < inbounds[j].Tag
		}
		return inbounds[i].ListenPort < inbounds[j].ListenPort
	})

	cfg := singboxConfig{
		Log: map[string]any{
			"level": "warn",
		},
		Inbounds: inbounds,
		Outbounds: []map[string]any{
			{
				"type": "direct",
				"tag":  "direct",
			},
		},
	}

	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return "", fmt.Errorf("marshal sing-box config: %w", err)
	}
	return string(data), nil
}

func inboundTag(user users.User) string {
	if user.InboundTag != "" {
		return user.InboundTag
	}
	return fmt.Sprintf("pulse-%s-%d", protocolOf(user), user.Port)
}

func protocolOf(user users.User) string {
	if user.Protocol == "" {
		return "vless"
	}
	return user.Protocol
}

func methodOf(user users.User) string {
	if protocolOf(user) != "shadowsocks" {
		return ""
	}
	if user.Method != "" {
		return user.Method
	}
	return "aes-128-gcm"
}

func transportFor(protocol string) map[string]any {
	return nil
}

func inboundPasswordFor(protocol, method string) string {
	switch protocol {
	case "shadowsocks":
		return "pulse-shared-secret"
	default:
		return ""
	}
}

func inboundUser(user users.User) []map[string]any {
	switch protocolOf(user) {
	case "trojan":
		return []map[string]any{{
			"name":     user.Username,
			"password": user.Secret,
		}}
	case "shadowsocks":
		return []map[string]any{{
			"name":     user.Username,
			"password": user.Secret,
		}}
	default:
		return []map[string]any{{
			"uuid": user.UUID,
			"name": user.Username,
		}}
	}
}
