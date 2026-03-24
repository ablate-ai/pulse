package config

import "os"

type Config struct {
	ServerAddr    string
	NodeAddr      string
	NodeAuthToken string
	DBPath        string
	WebDir        string
	AdminUsername string
	AdminPassword string
}

func Load() Config {
	return Config{
		ServerAddr:    envOrDefault("PULSE_SERVER_ADDR", ":8080"),
		NodeAddr:      envOrDefault("PULSE_NODE_ADDR", ":8081"),
		NodeAuthToken: envOrDefault("PULSE_NODE_AUTH_TOKEN", "dev-token"),
		DBPath:        envOrDefault("PULSE_DB_PATH", "./pulse.db"),
		WebDir:        envOrDefault("PULSE_WEB_DIR", ""),
		AdminUsername: envOrDefault("PULSE_ADMIN_USERNAME", "admin"),
		AdminPassword: envOrDefault("PULSE_ADMIN_PASSWORD", "admin"),
	}
}

func envOrDefault(key, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}

	return fallback
}
