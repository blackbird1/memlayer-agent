package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/redis/go-redis/v9"
	"google.golang.org/genai"
)

const (
	defaultMCPURL = "http://localhost:8000/mcp"
	port          = "9090"
	sessionTTL    = 30 * time.Minute
)

type ChatRequest struct {
	Message   string `json:"message"`
	SessionID string `json:"sessionId"`
}

type ChatStep struct {
	Type   string         `json:"type"` // "tool_call", "tool_result"
	Name   string         `json:"name,omitempty"`
	Args   map[string]any `json:"args,omitempty"`
	Result string         `json:"result,omitempty"`
}

type ChatResponse struct {
	Response string     `json:"response"`
	Steps    []ChatStep `json:"steps,omitempty"`
	Error    string     `json:"error,omitempty"`
}

type ToolExecutor func(ctx context.Context, args map[string]any) (string, error)

// PersistedContent mimics genai.Content for JSON serialization
type PersistedContent struct {
	Role  string          `json:"role"`
	Parts []PersistedPart `json:"parts"`
}

type PersistedPart struct {
	Type             string                 `json:"type"` // "text", "function_call", "function_response"
	Text             string                 `json:"text,omitempty"`
	FunctionCall     *PersistedFunctionCall `json:"function_call,omitempty"`
	FunctionResponse *PersistedFunctionResp `json:"function_response,omitempty"`
}

type PersistedFunctionCall struct {
	Name string         `json:"name"`
	Args map[string]any `json:"args"`
}

type PersistedFunctionResp struct {
	Name     string         `json:"name"`
	Response map[string]any `json:"response"`
}

var (
	logger      *slog.Logger
	redisClient *redis.Client
)

func init() {
	logger = slog.New(slog.NewJSONHandler(os.Stdout, nil))
}

func main() {
	defaultBaseURL := strings.TrimSpace(envOrDefault("MCP_URL", defaultMCPURL))
	defaultBearer := strings.TrimSpace(os.Getenv("MCP_BEARER_TOKEN"))

	redisAddr := envOrDefault("REDIS_ADDR", "localhost:6379")

	redisClient = redis.NewClient(&redis.Options{
		Addr: redisAddr,
	})

	if err := redisClient.Ping(context.Background()).Err(); err != nil {
		logger.Error("Failed to connect to Redis", "error", err)
	}

	apiKey := os.Getenv("GEMINI_API_KEY")
	if apiKey == "" {
		apiKey = os.Getenv("GOOGLE_API_KEY")
	}
	if apiKey == "" {
		logger.Error("GEMINI_API_KEY or GOOGLE_API_KEY is required")
		os.Exit(1)
	}

	http.HandleFunc("/api/chat", func(w http.ResponseWriter, r *http.Request) {
		enableCors(w)
		if r.Method == "OPTIONS" {
			return
		}

		if r.Method != "POST" {
			http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
			return
		}

		bearer := defaultBearer
		authHeader := r.Header.Get("Authorization")
		if strings.HasPrefix(authHeader, "Bearer ") {
			bearer = strings.TrimPrefix(authHeader, "Bearer ")
		}

		var req ChatRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			logger.Error("Invalid request body", "error", err)
			http.Error(w, "Invalid request body", http.StatusBadRequest)
			return
		}

		if req.SessionID == "" {
			req.SessionID = "default"
		}

		actualSessionID := req.SessionID
		if authHeader != "" {
			actualSessionID = fmt.Sprintf("%s-%s", req.SessionID, shortHash(bearer))
		}

		logger.Info("Received chat request", "sessionId", actualSessionID)

		steps, resp, err := handleChat(r.Context(), actualSessionID, req.Message, defaultBaseURL, bearer, apiKey)
		if err != nil {
			logger.Error("Error handling chat", "error", err, "sessionId", actualSessionID)
			json.NewEncoder(w).Encode(ChatResponse{Error: err.Error()})
			return
		}

		json.NewEncoder(w).Encode(ChatResponse{Response: resp, Steps: steps})
	})

	logger.Info("Server starting", "port", port)
	if err := http.ListenAndServe(":"+port, nil); err != nil {
		logger.Error("Server failed", "error", err)
		os.Exit(1)
	}
}

func handleChat(ctx context.Context, sessionID, message, baseURL, bearer, apiKey string) ([]ChatStep, string, error) {
	var agent *Agent
	var mcpTools []mcp.Tool

	if strings.TrimSpace(baseURL) != "" {
		createdAgent, err := NewAgent(ctx, baseURL, bearer)
		if err != nil {
			logger.Warn("MCP unavailable, continuing with local tools only", "error", err)
		} else {
			agent = createdAgent
			defer func() {
				if closeErr := agent.Close(); closeErr != nil {
					logger.Warn("Failed to close MCP client", "error", closeErr)
				}
			}()

			tools, err := agent.ListTools(ctx)
			if err != nil {
				logger.Warn("Failed to list MCP tools, continuing with local tools only", "error", err)
			} else {
				mcpTools = tools
			}
		}
	}

	client, err := genai.NewClient(ctx, &genai.ClientConfig{
		APIKey:  apiKey,
		Backend: genai.BackendGeminiAPI,
	})
	if err != nil {
		return nil, "", fmt.Errorf("failed to create genai client: %w", err)
	}

	modelName := "gemini-3-flash-preview"
	config := &genai.GenerateContentConfig{}

	funcDecls := make([]*genai.FunctionDeclaration, 0)
	toolExecutors := make(map[string]ToolExecutor)
	toolDisplayNames := make(map[string]string)

	for _, tool := range mcpTools {
		sanitizedName := strings.ReplaceAll(tool.Name, ".", "_")
		currentTool := tool
		funcDecls = append(funcDecls, &genai.FunctionDeclaration{
			Name:        sanitizedName,
			Description: currentTool.Description,
			Parameters:  convertSchema(currentTool.InputSchema),
		})
		toolDisplayNames[sanitizedName] = currentTool.Name
		if agent != nil {
			toolExecutors[sanitizedName] = func(execCtx context.Context, args map[string]any) (string, error) {
				return agent.CallTool(execCtx, currentTool.Name, args)
			}
		}
	}

	localDecls, localExecutors := buildLocalToolset()
	funcDecls = append(funcDecls, localDecls...)
	for name, executor := range localExecutors {
		toolExecutors[name] = executor
		if _, exists := toolDisplayNames[name]; !exists {
			toolDisplayNames[name] = name
		}
	}

	if len(funcDecls) > 0 {
		config.Tools = []*genai.Tool{{FunctionDeclarations: funcDecls}}
	}

	history, err := loadHistory(ctx, sessionID)
	if err != nil {
		logger.Error("Failed to load history", "error", err)
	}

	cs, err := client.Chats.Create(ctx, modelName, config, history)
	if err != nil {
		return nil, "", fmt.Errorf("failed to create chat session: %w", err)
	}

	resp, err := cs.SendMessage(ctx, genai.Part{Text: message})
	if err != nil {
		return nil, "", err
	}

	steps, fullResponse, err := processResponse(ctx, cs, resp, toolExecutors, toolDisplayNames)
	if err != nil {
		return nil, "", err
	}

	if err := saveHistory(ctx, sessionID, cs.History(false)); err != nil {
		logger.Error("Failed to save session history", "error", err)
	}

	return steps, fullResponse, nil
}

func processResponse(
	ctx context.Context,
	cs *genai.Chat,
	resp *genai.GenerateContentResponse,
	toolExecutors map[string]ToolExecutor,
	toolDisplayNames map[string]string,
) ([]ChatStep, string, error) {
	var fullResponse strings.Builder
	var steps []ChatStep

	for {
		hasToolCall := false
		for _, cand := range resp.Candidates {
			if cand.Content != nil {
				for _, part := range cand.Content.Parts {
					if part.Text != "" {
						fullResponse.WriteString(part.Text)
					}
					if part.FunctionCall != nil {
						hasToolCall = true
						fc := part.FunctionCall
						logger.Info("Tool call received", "tool", fc.Name, "args", fc.Args)
						toolName := normalizeToolCallName(fc.Name)
						executor, ok := toolExecutors[toolName]
						displayName := toolDisplayNames[toolName]
						if displayName == "" {
							displayName = toolName
						}
						if !ok {
							err := fmt.Errorf("tool %q is not available", toolName)
							steps = append(steps, ChatStep{
								Type: "tool_call",
								Name: displayName,
								Args: fc.Args,
							})
							steps = append(steps, ChatStep{
								Type:   "tool_result",
								Name:   displayName,
								Result: "Error: " + err.Error(),
							})

							resp, err = cs.SendMessage(ctx, genai.Part{
								FunctionResponse: &genai.FunctionResponse{
									Name:     fc.Name,
									Response: map[string]any{"error": err.Error()},
								},
							})
							if err != nil {
								return nil, "", fmt.Errorf("failed to send tool error response: %w", err)
							}
							goto NextResponse
						}

						steps = append(steps, ChatStep{
							Type: "tool_call",
							Name: displayName,
							Args: fc.Args,
						})

						result, err := executor(ctx, fc.Args)
						var toolResp genai.Part
						if err != nil {
							logger.Error("Tool execution failed", "tool", displayName, "error", err)
							steps = append(steps, ChatStep{
								Type:   "tool_result",
								Name:   displayName,
								Result: "Error: " + err.Error(),
							})
							toolResp = genai.Part{
								FunctionResponse: &genai.FunctionResponse{
									Name:     fc.Name,
									Response: map[string]any{"error": err.Error()},
								},
							}
						} else {
							logger.Info("Tool executed successfully", "tool", displayName)
							steps = append(steps, ChatStep{
								Type:   "tool_result",
								Name:   displayName,
								Result: result,
							})
							var jsonResult map[string]any
							if json.Unmarshal([]byte(result), &jsonResult) == nil {
								toolResp = genai.Part{
									FunctionResponse: &genai.FunctionResponse{
										Name:     fc.Name,
										Response: jsonResult,
									},
								}
							} else {
								toolResp = genai.Part{
									FunctionResponse: &genai.FunctionResponse{
										Name:     fc.Name,
										Response: map[string]any{"result": result},
									},
								}
							}
						}

						resp, err = cs.SendMessage(ctx, toolResp)
						if err != nil {
							return nil, "", fmt.Errorf("failed to send tool response: %w", err)
						}

						goto NextResponse
					}
				}
			}
		}

		if !hasToolCall {
			break
		}

	NextResponse:
		continue
	}

	return steps, fullResponse.String(), nil
}

// --- Persistence Helpers ---

func saveHistory(ctx context.Context, sessionID string, history []*genai.Content) error {
	var persisted []*PersistedContent
	for _, c := range history {
		pc := &PersistedContent{Role: c.Role}
		for _, p := range c.Parts {
			pp := PersistedPart{}
			if p.Text != "" {
				pp.Type = "text"
				pp.Text = p.Text
			} else if p.FunctionCall != nil {
				pp.Type = "function_call"
				pp.FunctionCall = &PersistedFunctionCall{Name: p.FunctionCall.Name, Args: p.FunctionCall.Args}
			} else if p.FunctionResponse != nil {
				pp.Type = "function_response"
				pp.FunctionResponse = &PersistedFunctionResp{Name: p.FunctionResponse.Name, Response: p.FunctionResponse.Response}
			} else {
				continue
			}
			pc.Parts = append(pc.Parts, pp)
		}
		persisted = append(persisted, pc)
	}

	data, err := json.Marshal(persisted)
	if err != nil {
		return err
	}

	return redisClient.Set(ctx, "session:"+sessionID, data, sessionTTL).Err()
}

func loadHistory(ctx context.Context, sessionID string) ([]*genai.Content, error) {
	data, err := redisClient.Get(ctx, "session:"+sessionID).Bytes()
	if err != nil {
		if err == redis.Nil {
			return nil, nil // No history found
		}
		return nil, err
	}

	var persisted []*PersistedContent
	if err := json.Unmarshal(data, &persisted); err != nil {
		return nil, err
	}

	var history []*genai.Content
	for _, pc := range persisted {
		role := pc.Role
		if role == "" {
			role = "user"
		}
		c := &genai.Content{Role: role}
		for _, pp := range pc.Parts {
			part := &genai.Part{}
			switch pp.Type {
			case "text":
				part.Text = pp.Text
			case "function_call":
				if pp.FunctionCall != nil {
					part.FunctionCall = &genai.FunctionCall{Name: pp.FunctionCall.Name, Args: pp.FunctionCall.Args}
				}
			case "function_response":
				if pp.FunctionResponse != nil {
					part.FunctionResponse = &genai.FunctionResponse{Name: pp.FunctionResponse.Name, Response: pp.FunctionResponse.Response}
				}
			}
			if part.Text != "" || part.FunctionCall != nil || part.FunctionResponse != nil {
				c.Parts = append(c.Parts, part)
			}
		}
		history = append(history, c)
	}

	return history, nil
}

// --- Utils ---

func enableCors(w http.ResponseWriter) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("Access-Control-Allow-Methods", "POST, OPTIONS")
	w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization")
}

func envOrDefault(key, fallback string) string {
	value := os.Getenv(key)
	if strings.TrimSpace(value) == "" {
		return fallback
	}
	return value
}

func shortHash(s string) string {
	if len(s) > 8 {
		return s[:8]
	}
	return s
}

func normalizeToolCallName(name string) string {
	trimmed := strings.TrimSpace(name)
	if strings.Contains(trimmed, ":") {
		parts := strings.Split(trimmed, ":")
		return parts[len(parts)-1]
	}
	return trimmed
}

func convertSchema(inputSchema mcp.ToolInputSchema) *genai.Schema {
	properties := make(map[string]*genai.Schema)
	if inputSchema.Properties != nil {
		for name, prop := range inputSchema.Properties {
			if propMap, ok := prop.(map[string]interface{}); ok {
				properties[name] = convertProperty(propMap)
			}
		}
	}

	return &genai.Schema{
		Type:       genai.TypeObject,
		Properties: properties,
		Required:   inputSchema.Required,
	}
}

func convertProperty(prop map[string]interface{}) *genai.Schema {
	s := &genai.Schema{}

	if t, ok := prop["type"].(string); ok {
		switch t {
		case "string":
			s.Type = genai.TypeString
		case "number":
			s.Type = genai.TypeNumber
		case "integer":
			s.Type = genai.TypeInteger
		case "boolean":
			s.Type = genai.TypeBoolean
		case "array":
			s.Type = genai.TypeArray
		case "object":
			s.Type = genai.TypeObject
		}
	}

	if desc, ok := prop["description"].(string); ok {
		s.Description = desc
	}

	if format, ok := prop["format"].(string); ok {
		s.Format = format
	}

	if s.Type == genai.TypeArray {
		if items, ok := prop["items"].(map[string]interface{}); ok {
			s.Items = convertProperty(items)
		}
	}

	if s.Type == genai.TypeObject {
		if props, ok := prop["properties"].(map[string]interface{}); ok {
			s.Properties = make(map[string]*genai.Schema)
			for k, v := range props {
				if vMap, ok := v.(map[string]interface{}); ok {
					s.Properties[k] = convertProperty(vMap)
				}
			}
		}
		if req, ok := prop["required"].([]interface{}); ok {
			for _, r := range req {
				if rStr, ok := r.(string); ok {
					s.Required = append(s.Required, rStr)
				}
			}
		}
	}

	return s
}
