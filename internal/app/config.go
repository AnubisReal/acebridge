package app

import (
	"os"
	"strings"
)

type Config struct {
	ListenAddr          string
	DataDir             string
	AllowPrivateSources bool
	IPTVOrgAPIBase      string
}

func ConfigFromEnv() Config {
	return Config{
		ListenAddr:          envOr("ACEBRIDGE_LISTEN_ADDR", ":8080"),
		DataDir:             envOr("ACEBRIDGE_DATA_DIR", "./data"),
		AllowPrivateSources: strings.EqualFold(os.Getenv("ACEBRIDGE_ALLOW_PRIVATE_SOURCES"), "true"),
		IPTVOrgAPIBase:      envOr("ACEBRIDGE_IPTV_ORG_API", "https://iptv-org.github.io/api"),
	}
}

func envOr(key, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return fallback
}
