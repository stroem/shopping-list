// Package config loads server configuration from the environment.
package config

import (
	"errors"
	"os"
)

// Config is the server's runtime configuration, sourced entirely from env vars.
type Config struct {
	DatabaseURL  string // required; standard postgres:// URL
	Port         string // listen port, default "8080"
	OIDCIssuer   string // OIDC issuer, default https://accounts.google.com
	OIDCAudience string // OIDC audience (Google client id); empty ⇒ auth disabled
}

// Load reads configuration from the environment. DATABASE_URL is required.
func Load() (Config, error) {
	cfg := Config{
		DatabaseURL: os.Getenv("DATABASE_URL"),
		Port:        os.Getenv("PORT"),
	}
	if cfg.DatabaseURL == "" {
		return Config{}, errors.New("DATABASE_URL is required")
	}
	if cfg.Port == "" {
		cfg.Port = "8080"
	}
	cfg.OIDCIssuer = os.Getenv("OIDC_ISSUER")
	if cfg.OIDCIssuer == "" {
		cfg.OIDCIssuer = "https://accounts.google.com"
	}
	cfg.OIDCAudience = os.Getenv("OIDC_AUDIENCE")
	return cfg, nil
}
