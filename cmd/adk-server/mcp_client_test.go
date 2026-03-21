package main

import "testing"

func TestAuthorizationHeaderValue(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		token string
		want  string
	}{
		{name: "raw token", token: "abc123", want: "Bearer abc123"},
		{name: "prefixed token", token: "Bearer abc123", want: "Bearer abc123"},
		{name: "lowercase prefix", token: "bearer abc123", want: "bearer abc123"},
		{name: "trimmed token", token: "  abc123  ", want: "Bearer abc123"},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := authorizationHeaderValue(tt.token); got != tt.want {
				t.Fatalf("authorizationHeaderValue(%q) = %q, want %q", tt.token, got, tt.want)
			}
		})
	}
}

func TestResolveMCPHeadersPrefersConfigBearerToken(t *testing.T) {
	t.Setenv("MEMLAYER_MCP_BEARER_TOKEN", "env-token")
	t.Setenv("MCP_BEARER_TOKEN", "legacy-env-token")

	headers := resolveMCPHeaders(MCPServerConfig{
		BearerToken: "config-token",
	})

	if got := headers["Authorization"]; got != "Bearer config-token" {
		t.Fatalf("Authorization header = %q, want %q", got, "Bearer config-token")
	}
}

func TestResolveMCPHeadersUsesEnvFallback(t *testing.T) {
	t.Setenv("MEMLAYER_MCP_BEARER_TOKEN", "env-token")
	t.Setenv("MCP_BEARER_TOKEN", "legacy-env-token")

	headers := resolveMCPHeaders(MCPServerConfig{})

	if got := headers["Authorization"]; got != "Bearer env-token" {
		t.Fatalf("Authorization header = %q, want %q", got, "Bearer env-token")
	}
}

func TestResolveMCPHeadersPreservesExplicitAuthorizationHeader(t *testing.T) {
	t.Setenv("MEMLAYER_MCP_BEARER_TOKEN", "env-token")

	headers := resolveMCPHeaders(MCPServerConfig{
		Headers: map[string]string{
			"Authorization": "Bearer explicit-token",
		},
		BearerToken: "config-token",
	})

	if got := headers["Authorization"]; got != "Bearer explicit-token" {
		t.Fatalf("Authorization header = %q, want %q", got, "Bearer explicit-token")
	}
}
