package proxycfg

import (
	"strings"
	"testing"

	"pulse/internal/users"
)

func TestBuildVLESSSingboxConfigOmitsUnsupportedFieldsForVLESS(t *testing.T) {
	config, err := BuildVLESSSingboxConfig([]users.User{{
		ID:       "u1",
		Username: "alice",
		UUID:     "11111111-1111-1111-1111-111111111111",
		Protocol: "vless",
		Enabled:  true,
		NodeID:   "node-1",
		Domain:   "example.com",
		Port:     39001,
	}})
	if err != nil {
		t.Fatalf("BuildVLESSSingboxConfig() error = %v", err)
	}

	if strings.Contains(config, `"transport"`) {
		t.Fatalf("unexpected transport in config: %s", config)
	}
	if strings.Contains(config, `"method"`) {
		t.Fatalf("unexpected method in config: %s", config)
	}
	if strings.Contains(config, `"password"`) {
		t.Fatalf("unexpected password in config: %s", config)
	}
}

func TestBuildVLESSSingboxConfigKeepsShadowsocksMethod(t *testing.T) {
	config, err := BuildVLESSSingboxConfig([]users.User{{
		ID:       "u1",
		Username: "alice",
		Protocol: "shadowsocks",
		Secret:   "secret",
		Method:   "aes-256-gcm",
		Enabled:  true,
		NodeID:   "node-1",
		Domain:   "example.com",
		Port:     39002,
	}})
	if err != nil {
		t.Fatalf("BuildVLESSSingboxConfig() error = %v", err)
	}

	if !strings.Contains(config, `"method": "aes-256-gcm"`) {
		t.Fatalf("expected shadowsocks method in config: %s", config)
	}
}
