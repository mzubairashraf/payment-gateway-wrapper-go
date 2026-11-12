package config

import (
	"strings"
	"testing"
)

func TestLoad(t *testing.T) {
	const token = "0123456789abcdef0123"
	tests := []struct {
		name    string
		env     map[string]string
		wantErr string
		check   func(*testing.T, *Config)
	}{
		{name: "simulator only", env: map[string]string{"API_TOKEN": token, "ENABLE_SIMULATOR": "true"},
			check: func(t *testing.T, c *Config) {
				if !c.Simulator || c.AuthorizeNet || c.Addr != "127.0.0.1:8080" || c.AuthNetCurrency != "USD" {
					t.Errorf("config = %+v", c)
				}
			}},
		{name: "authorize.net", env: map[string]string{"API_TOKEN": token, "AUTHNET_API_LOGIN_ID": "id", "AUTHNET_TRANSACTION_KEY": "key"},
			check: func(t *testing.T, c *Config) {
				if !c.AuthorizeNet || c.Simulator {
					t.Errorf("config = %+v", c)
				}
			}},
		{name: "no token", env: map[string]string{"ENABLE_SIMULATOR": "true"}, wantErr: "API_TOKEN"},
		{name: "short token", env: map[string]string{"API_TOKEN": "short", "ENABLE_SIMULATOR": "true"}, wantErr: "API_TOKEN"},
		{name: "no gateway", env: map[string]string{"API_TOKEN": token}, wantErr: "no gateway is enabled"},
		{name: "half credentials", env: map[string]string{"API_TOKEN": token, "AUTHNET_API_LOGIN_ID": "id"}, wantErr: "set both"},
		{name: "plain http endpoint", env: map[string]string{"API_TOKEN": token, "ENABLE_SIMULATOR": "1", "AUTHNET_ENDPOINT": "http://example.com"}, wantErr: "https"},
		{name: "bad timeout", env: map[string]string{"API_TOKEN": token, "ENABLE_SIMULATOR": "1", "GATEWAY_TIMEOUT": "soon"}, wantErr: "GATEWAY_TIMEOUT"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg, err := Load(func(k string) string { return tt.env[k] })
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("err = %v, want containing %q", err, tt.wantErr)
				}
				for _, secret := range []string{tt.env["API_TOKEN"], tt.env["AUTHNET_TRANSACTION_KEY"]} {
					if len(secret) > 5 && strings.Contains(err.Error(), secret) {
						t.Errorf("error discloses a secret: %v", err)
					}
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			tt.check(t, cfg)
		})
	}
}
