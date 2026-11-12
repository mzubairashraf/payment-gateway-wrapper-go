// Package config reads configuration from the environment. Credentials are
// never read from files or flags, and are never printed.
package config

import (
	"errors"
	"net/url"
	"strings"
	"time"
)

// Config is the validated runtime configuration.
type Config struct {
	Addr     string
	APIToken string

	Simulator bool // enable the in-memory simulator gateway

	AuthorizeNet        bool // true when both credentials are present
	AuthNetLoginID      string
	AuthNetKey          string
	AuthNetEndpoint     string
	AuthNetCurrency     string
	GatewayTimeout      time.Duration
	ShutdownGracePeriod time.Duration
}

// Load reads and validates the environment.
func Load(getenv func(string) string) (*Config, error) {
	env := func(key, fallback string) string {
		if v := strings.TrimSpace(getenv(key)); v != "" {
			return v
		}
		return fallback
	}
	cfg := &Config{
		Addr:                env("ADDR", "127.0.0.1:8080"),
		APIToken:            env("API_TOKEN", ""),
		Simulator:           isTrue(env("ENABLE_SIMULATOR", "")),
		AuthNetLoginID:      env("AUTHNET_API_LOGIN_ID", ""),
		AuthNetKey:          env("AUTHNET_TRANSACTION_KEY", ""),
		AuthNetEndpoint:     env("AUTHNET_ENDPOINT", ""),
		AuthNetCurrency:     strings.ToUpper(env("AUTHNET_CURRENCY", "USD")),
		ShutdownGracePeriod: 25 * time.Second,
	}

	timeout, err := time.ParseDuration(env("GATEWAY_TIMEOUT", "20s"))
	if err != nil || timeout <= 0 {
		return nil, errors.New("GATEWAY_TIMEOUT must be a positive duration such as 20s")
	}
	cfg.GatewayTimeout = timeout

	if len(cfg.APIToken) < 16 {
		return nil, errors.New("API_TOKEN must be set to at least 16 characters; every request must present it as a bearer token")
	}

	switch {
	case cfg.AuthNetLoginID != "" && cfg.AuthNetKey != "":
		cfg.AuthorizeNet = true
	case cfg.AuthNetLoginID != "" || cfg.AuthNetKey != "":
		return nil, errors.New("set both AUTHNET_API_LOGIN_ID and AUTHNET_TRANSACTION_KEY, or neither")
	}
	if cfg.AuthNetEndpoint != "" {
		if u, err := url.Parse(cfg.AuthNetEndpoint); err != nil || u.Scheme != "https" || u.Host == "" {
			return nil, errors.New("AUTHNET_ENDPOINT must be an https URL")
		}
	}
	if !cfg.AuthorizeNet && !cfg.Simulator {
		return nil, errors.New("no gateway is enabled: set ENABLE_SIMULATOR=true or the AUTHNET_* credentials")
	}
	return cfg, nil
}

func isTrue(v string) bool {
	switch strings.ToLower(v) {
	case "1", "true", "yes", "on":
		return true
	}
	return false
}
