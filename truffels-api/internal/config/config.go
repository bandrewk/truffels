package config

import "os"

type Config struct {
	Listen       string
	DBPath       string
	ComposeRoot  string
	ConfigRoot   string
	SecretsRoot  string
	HostProc     string
	HostSys      string
	DataRoot     string
	GitHubRepo   string // owner/repo for self-update checks (e.g. "bandrewk/Project-Truffels")
}

func Load() *Config {
	return &Config{
		Listen:      envOr("TRUFFELS_LISTEN", ":8080"),
		DBPath:      envOr("TRUFFELS_DB_PATH", "/data/truffels.db"),
		ComposeRoot: envOr("TRUFFELS_COMPOSE_ROOT", "/srv/truffels/compose"),
		ConfigRoot:  envOr("TRUFFELS_CONFIG_ROOT", "/srv/truffels/config"),
		SecretsRoot: envOr("TRUFFELS_SECRETS_ROOT", "/srv/truffels/secrets"),
		HostProc:    envOr("TRUFFELS_HOST_PROC", "/proc"),
		HostSys:     envOr("TRUFFELS_HOST_SYS", "/sys"),
		DataRoot:    envOr("TRUFFELS_DATA_ROOT", "/srv/truffels/data"),
		GitHubRepo:  envOr("TRUFFELS_GITHUB_REPO", "bandrewk/Project-Truffels"),
	}
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
