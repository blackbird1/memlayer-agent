package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"

	"github.com/mark3labs/mcp-go/client"
	"github.com/mark3labs/mcp-go/client/transport"
	"github.com/mark3labs/mcp-go/mcp"
	"google.golang.org/genai"
)

type MCPSettings struct {
	MCPServers map[string]MCPServerConfig `json:"mcpServers"`
}

type MCPServerConfig struct {
	// Stdio-based config
	Command string            `json:"command,omitempty"`
	Args    []string          `json:"args,omitempty"`
	Env     map[string]string `json:"env,omitempty"`

	// SSE-based config
	URL     string            `json:"url,omitempty"`
	Type    string            `json:"type,omitempty"` // "http" or "sse"
	Headers map[string]string `json:"headers,omitempty"`
}

type MCPServerManager struct {
	clients map[string]*client.Client
	logger  *slog.Logger
}

func NewMCPServerManager(logger *slog.Logger) *MCPServerManager {
	return &MCPServerManager{
		clients: make(map[string]*client.Client),
		logger:  logger,
	}
}

func (m *MCPServerManager) LoadAndConnect(ctx context.Context) error {
	settings, err := m.discoverSettings()
	if err != nil {
		m.logger.Warn("No MCP settings found or failed to load", "error", err)
		// Fallback to environment variables if no config file found
		mcpURL := os.Getenv("MCP_URL")
		if mcpURL != "" {
			m.logger.Info("Falling back to MCP_URL from environment")
			cfg := MCPServerConfig{
				URL:  mcpURL,
				Type: "sse",
			}
			if token := os.Getenv("MCP_BEARER_TOKEN"); token != "" {
				cfg.Headers = map[string]string{"Authorization": "Bearer " + token}
			}
			return m.connectServer(ctx, "default", cfg)
		}
		return nil
	}

	for name, cfg := range settings.MCPServers {
		if err := m.connectServer(ctx, name, cfg); err != nil {
			m.logger.Error("Failed to connect to MCP server", "name", name, "error", err)
		}
	}

	return nil
}

func (m *MCPServerManager) discoverSettings() (*MCPSettings, error) {
	paths := []string{
		os.Getenv("MCP_SETTINGS_PATH"),
		"mcp_settings.json",
		".mcp_settings.json",
		".gemini/settings.json",
	}

	for _, p := range paths {
		if p == "" {
			continue
		}
		absPath, _ := filepath.Abs(p)
		if _, err := os.Stat(absPath); err == nil {
			m.logger.Info("Loading MCP settings", "path", absPath)
			data, err := os.ReadFile(absPath)
			if err != nil {
				return nil, err
			}
			var settings MCPSettings
			if err := json.Unmarshal(data, &settings); err != nil {
				return nil, err
			}
			return &settings, nil
		}
	}

	return nil, fmt.Errorf("no mcp settings file found")
}

func (m *MCPServerManager) connectServer(ctx context.Context, name string, cfg MCPServerConfig) error {
	var mcpClient *client.Client

	if cfg.Command != "" {
		// Stdio client
		env := make([]string, 0, len(cfg.Env))
		for k, v := range cfg.Env {
			env = append(env, fmt.Sprintf("%s=%s", k, v))
		}
		// Inherit current environment if not specified? 
		// Standard MCP usually merges. Let's merge with current env for better UX.
		for _, e := range os.Environ() {
			env = append(env, e)
		}

		c, err := client.NewStdioMCPClient(cfg.Command, env, cfg.Args...)
		if err != nil {
			return fmt.Errorf("stdio client: %w", err)
		}
		mcpClient = c
	} else if cfg.URL != "" {
		// SSE client
		trans, err := transport.NewSSE(cfg.URL, transport.WithHeaders(cfg.Headers))
		if err != nil {
			return fmt.Errorf("sse transport: %w", err)
		}
		mcpClient = client.NewClient(trans)
		if err := mcpClient.Start(ctx); err != nil {
			return fmt.Errorf("sse start: %w", err)
		}
	} else {
		return fmt.Errorf("invalid config: missing command or url")
	}

	initRequest := mcp.InitializeRequest{
		Params: mcp.InitializeParams{
			ProtocolVersion: mcp.LATEST_PROTOCOL_VERSION,
			ClientInfo: mcp.Implementation{
				Name:    "adk-server",
				Version: "1.0.0",
			},
		},
	}

	_, err := mcpClient.Initialize(ctx, initRequest)
	if err != nil {
		return fmt.Errorf("initialize: %w", err)
	}

	m.clients[name] = mcpClient
	m.logger.Info("Connected to MCP server", "name", name)
	return nil
}

func (m *MCPServerManager) ListAllTools(ctx context.Context) ([]*genai.FunctionDeclaration, map[string]ToolExecutor, error) {
	var allDecls []*genai.FunctionDeclaration
	allExecutors := make(map[string]ToolExecutor)

	for name, mcpClient := range m.clients {
		resp, err := mcpClient.ListTools(ctx, mcp.ListToolsRequest{})
		if err != nil {
			m.logger.Error("Failed to list tools for MCP server", "name", name, "error", err)
			continue
		}

		for _, tool := range resp.Tools {
			// Naming collision handling: prefix with name if multiple servers?
			// Standard MCP usually uses unique names or namespacing.
			// For now, let's keep it simple.
			decl := &genai.FunctionDeclaration{
				Name:        tool.Name,
				Description: tool.Description,
				Parameters:  convertMCPArgumentsToGenaiSchema(tool.InputSchema),
			}
			allDecls = append(allDecls, decl)
			allExecutors[tool.Name] = m.makeExecutor(mcpClient, tool.Name)
		}
	}

	return allDecls, allExecutors, nil
}

func (m *MCPServerManager) makeExecutor(mcpClient *client.Client, toolName string) ToolExecutor {
	return func(ctx context.Context, args map[string]any) (string, error) {
		resp, err := mcpClient.CallTool(ctx, mcp.CallToolRequest{
			Params: mcp.CallToolParams{
				Name:      toolName,
				Arguments: args,
			},
		})
		if err != nil {
			return "", fmt.Errorf("call tool %s: %w", toolName, err)
		}

		if resp.IsError {
			return "", fmt.Errorf("tool %s returned error: %+v", toolName, resp.Content)
		}

		var parts []string
		for _, item := range resp.Content {
			switch v := item.(type) {
			case mcp.TextContent:
				parts = append(parts, v.Text)
			case *mcp.TextContent:
				parts = append(parts, v.Text)
			case mcp.EmbeddedResource:
				parts = append(parts, fmt.Sprintf("%v", v.Resource))
			default:
				parts = append(parts, fmt.Sprintf("%v", v))
			}
		}
		return strings.Join(parts, "\n"), nil
	}
}

func convertMCPArgumentsToGenaiSchema(schema mcp.ToolInputSchema) *genai.Schema {
	res := &genai.Schema{
		Type: mapMCPTypeToGenai(schema.Type),
	}
	if schema.Properties != nil {
		res.Properties = make(map[string]*genai.Schema)
		for k, v := range schema.Properties {
			if propMap, ok := v.(map[string]any); ok {
				res.Properties[k] = convertAnyToGenaiSchema(propMap)
			}
		}
	}
	res.Required = schema.Required
	return res
}

func convertAnyToGenaiSchema(propMap map[string]any) *genai.Schema {
	s := &genai.Schema{}
	if t, ok := propMap["type"].(string); ok {
		s.Type = mapMCPTypeToGenai(t)
	}
	if d, ok := propMap["description"].(string); ok {
		s.Description = d
	}
	if s.Type == genai.TypeObject {
		if props, ok := propMap["properties"].(map[string]any); ok {
			s.Properties = make(map[string]*genai.Schema)
			for k, v := range props {
				if pm, ok := v.(map[string]any); ok {
					s.Properties[k] = convertAnyToGenaiSchema(pm)
				}
			}
		}
		if req, ok := propMap["required"].([]any); ok {
			for _, r := range req {
				if rs, ok := r.(string); ok {
					s.Required = append(s.Required, rs)
				}
			}
		}
	}
	if s.Type == genai.TypeArray {
		if items, ok := propMap["items"].(map[string]any); ok {
			s.Items = convertAnyToGenaiSchema(items)
		}
	}
	return s
}

func mapMCPTypeToGenai(mcpType string) genai.Type {
	switch strings.ToLower(mcpType) {
	case "string":
		return genai.TypeString
	case "number":
		return genai.TypeNumber
	case "integer":
		return genai.TypeInteger
	case "boolean":
		return genai.TypeBoolean
	case "array":
		return genai.TypeArray
	case "object":
		return genai.TypeObject
	default:
		return genai.TypeUnspecified
	}
}
