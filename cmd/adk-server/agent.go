package main

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/mark3labs/mcp-go/client"
	"github.com/mark3labs/mcp-go/client/transport"
	"github.com/mark3labs/mcp-go/mcp"
)

type Agent struct {
	client *client.Client
}

func NewAgent(ctx context.Context, baseURL, bearer string) (*Agent, error) {
	headers := map[string]string{}
	if bearer != "" {
		headers["Authorization"] = fmt.Sprintf("Bearer %s", bearer)
	}

	mcpClient, err := client.NewStreamableHttpClient(
		baseURL,
		transport.WithHTTPHeaders(headers),
	)
	if err != nil {
		return nil, fmt.Errorf("create MCP client: %w", err)
	}

	if err := mcpClient.Start(ctx); err != nil {
		return nil, fmt.Errorf("start MCP client: %w", err)
	}

	_, err = mcpClient.Initialize(ctx, mcp.InitializeRequest{
		Params: mcp.InitializeParams{
			ProtocolVersion: mcp.LATEST_PROTOCOL_VERSION,
			ClientInfo: mcp.Implementation{
				Name:    "adk-interview-cli",
				Version: "0.1.0",
			},
			Capabilities: mcp.ClientCapabilities{},
		},
	})
	if err != nil {
		return nil, fmt.Errorf("initialize MCP client: %w", err)
	}

	return &Agent{client: mcpClient}, nil
}

func (a *Agent) Close() error {
	return a.client.Close()
}

func (a *Agent) ListTools(ctx context.Context) ([]mcp.Tool, error) {
	result, err := a.client.ListTools(ctx, mcp.ListToolsRequest{})
	if err != nil {
		return nil, fmt.Errorf("list tools: %w", err)
	}

	return result.Tools, nil
}

func (a *Agent) CallTool(ctx context.Context, name string, args map[string]interface{}) (string, error) {
	result, err := a.client.CallTool(ctx, mcp.CallToolRequest{
		Params: mcp.CallToolParams{
			Name:      name,
			Arguments: args,
		},
	})
	if err != nil {
		return "", fmt.Errorf("call tool %s: %w", name, err)
	}

	return formatToolResult(result), nil
}

func formatToolResult(result *mcp.CallToolResult) string {
	if result == nil {
		return ""
	}

	parts := make([]string, 0, len(result.Content))
	for _, content := range result.Content {
		switch typed := content.(type) {
		case mcp.TextContent:
			parts = append(parts, typed.Text)
		default:
			payload, err := json.MarshalIndent(typed, "", "  ")
			if err != nil {
				parts = append(parts, fmt.Sprintf("%v", typed))
				continue
			}
			parts = append(parts, string(payload))
		}
	}

	return strings.TrimSpace(strings.Join(parts, "\n"))
}
