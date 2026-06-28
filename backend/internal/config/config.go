// Package config loads server configuration from the environment.
package config

import (
	"errors"
	"os"
)

// Config is the server's runtime configuration, sourced entirely from env vars.
type Config struct {
	DatabaseURL string // required; standard postgres:// URL
	Port        string // listen port, default "8080"
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
	return cfg, nil
}
