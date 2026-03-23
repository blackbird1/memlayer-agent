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

	"github.com/redis/go-redis/v9"
	"google.golang.org/genai"
)

const (
	port            = "9090"
	sessionTTL      = 30 * time.Minute
	assistantPrompt = `You are a concise assistant.

Use available MCP tools when they help answer the user's question.
If no relevant tool is available, answer directly and clearly state any limits.
Keep responses short, structured, and grounded in tool results when tools are used.`
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
	mcpManager  *MCPServerManager
)

func init() {
	logger = slog.New(slog.NewJSONHandler(os.Stdout, nil))
}

func main() {
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

	mcpManager = NewMCPServerManager(logger)
	// Use a background context with timeout for initial connection
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()
	if err := mcpManager.LoadAndConnect(ctx); err != nil {
		logger.Error("Failed to initialize MCP manager", "error", err)
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

		var req ChatRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			logger.Error("Invalid request body", "error", err)
			http.Error(w, "Invalid request body", http.StatusBadRequest)
			return
		}

		if req.SessionID == "" {
			req.SessionID = "default"
		}

		logger.Info("Received chat request", "sessionId", req.SessionID)

		steps, resp, err := handleChat(r.Context(), req.SessionID, req.Message, apiKey)

		if err != nil {
			logger.Error("Error handling chat", "error", err, "sessionId", req.SessionID)
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

func handleChat(ctx context.Context, sessionID, message, apiKey string) ([]ChatStep, string, error) {
	startedAt := time.Now()
	logger.Info("handleChat started", "sessionId", sessionID, "message_chars", len(strings.TrimSpace(message)))

	client, err := genai.NewClient(ctx, &genai.ClientConfig{
		APIKey:  apiKey,
		Backend: genai.BackendGeminiAPI,
	})
	if err != nil {
		logger.Error("Failed to create genai client", "sessionId", sessionID, "error", err)
		return nil, "", fmt.Errorf("failed to create genai client: %w", err)
	}

	modelName := "gemini-3-flash-preview"
	config := &genai.GenerateContentConfig{
		SystemInstruction: &genai.Content{
			Parts: []*genai.Part{
				{Text: assistantPrompt},
			},
		},
	}

	funcDecls := make([]*genai.FunctionDeclaration, 0)
	toolExecutors := make(map[string]ToolExecutor)
	toolDisplayNames := make(map[string]string)

	// Register remote MCP tools if available.
	if mcpManager != nil {
		mcpDecls, mcpExecutors, err := mcpManager.ListAllTools(ctx)
		if err != nil {
			logger.Error("Failed to list MCP tools", "sessionId", sessionID, "error", err)
		} else {
			funcDecls = append(funcDecls, mcpDecls...)
			for name, executor := range mcpExecutors {
				toolExecutors[name] = executor
				if _, exists := toolDisplayNames[name]; !exists {
					toolDisplayNames[name] = name
				}
			}
			logger.Info("MCP tools registered", "sessionId", sessionID, "count", len(mcpDecls))
		}
	}

	// Attach tool declarations to model config so function-calling is enabled.
	if len(funcDecls) > 0 {
		config.Tools = []*genai.Tool{{FunctionDeclarations: funcDecls}}
	}
	logger.Info("Chat tools configured", "sessionId", sessionID, "tool_count", len(funcDecls))

	history, err := loadHistory(ctx, sessionID)
	if err != nil {
		logger.Error("Failed to load history", "sessionId", sessionID, "error", err)
	}
	logger.Info("Chat history loaded", "sessionId", sessionID, "history_items", len(history))

	cs, err := client.Chats.Create(ctx, modelName, config, history)
	if err != nil {
		logger.Error("Failed to create chat session", "sessionId", sessionID, "error", err)
		return nil, "", fmt.Errorf("failed to create chat session: %w", err)
	}

	resp, err := cs.SendMessage(ctx, genai.Part{Text: message})
	if err != nil {
		logger.Error("Initial model message failed", "sessionId", sessionID, "error", err)
		return nil, "", err
	}

	steps, fullResponse, err := processResponse(ctx, cs, resp, toolExecutors, toolDisplayNames)
	if err != nil {
		logger.Error("processResponse failed", "sessionId", sessionID, "error", err)
		return nil, "", err
	}

	if err := saveHistory(ctx, sessionID, cs.History(false)); err != nil {
		logger.Error("Failed to save session history", "sessionId", sessionID, "error", err)
	}
	logger.Info("handleChat completed", "sessionId", sessionID, "steps", len(steps), "response_chars", len(strings.TrimSpace(fullResponse)), "duration_ms", time.Since(startedAt).Milliseconds())

	return steps, fullResponse, nil
}

func processResponse(
	ctx context.Context,
	cs *genai.Chat,
	resp *genai.GenerateContentResponse,
	toolExecutors map[string]ToolExecutor,
	toolDisplayNames map[string]string,
) ([]ChatStep, string, error) {
	startedAt := time.Now()
	var fullResponse strings.Builder
	var steps []ChatStep
	toolRoundTrips := 0

	for {
		nextResp, handledToolCall, err := processModelTurn(ctx, cs, resp, toolExecutors, toolDisplayNames, &fullResponse, &steps)
		if err != nil {
			return nil, "", err
		}
		if !handledToolCall {
			break
		}
		toolRoundTrips++
		resp = nextResp
	}

	logger.Info("processResponse completed", "steps", len(steps), "tool_round_trips", toolRoundTrips, "response_chars", len(strings.TrimSpace(fullResponse.String())), "duration_ms", time.Since(startedAt).Milliseconds())
	return steps, fullResponse.String(), nil
}

func processModelTurn(
	ctx context.Context,
	cs *genai.Chat,
	resp *genai.GenerateContentResponse,
	toolExecutors map[string]ToolExecutor,
	toolDisplayNames map[string]string,
	fullResponse *strings.Builder,
	steps *[]ChatStep,
) (*genai.GenerateContentResponse, bool, error) {
	for _, cand := range resp.Candidates {
		if cand.Content == nil {
			continue
		}
		for _, part := range cand.Content.Parts {
			if part.Text != "" {
				fullResponse.WriteString(part.Text)
			}
			if part.FunctionCall != nil {
				nextResp, err := handleFunctionCallPart(ctx, cs, part.FunctionCall, toolExecutors, toolDisplayNames, steps)
				if err != nil {
					return nil, false, err
				}
				return nextResp, true, nil
			}
		}
	}
	return nil, false, nil
}

func handleFunctionCallPart(
	ctx context.Context,
	cs *genai.Chat,
	fc *genai.FunctionCall,
	toolExecutors map[string]ToolExecutor,
	toolDisplayNames map[string]string,
	steps *[]ChatStep,
) (*genai.GenerateContentResponse, error) {
	logger.Info("Tool call received", "tool", fc.Name, "args", fc.Args)
	toolName := normalizeToolCallName(fc.Name)
	displayName := toolDisplayNames[toolName]
	if displayName == "" {
		displayName = toolName
	}

	*steps = append(*steps, ChatStep{
		Type: "tool_call",
		Name: displayName,
		Args: fc.Args,
	})

	executor, ok := toolExecutors[toolName]
	if !ok {
		toolErr := fmt.Errorf("tool %q is not available", toolName)
		*steps = append(*steps, ChatStep{
			Type:   "tool_result",
			Name:   displayName,
			Result: "Error: " + toolErr.Error(),
		})
		return sendToolResponse(ctx, cs, fc.Name, map[string]any{"error": toolErr.Error()}, "failed to send tool error response")
	}

	result, err := executor(ctx, fc.Args)
	if err != nil {
		logger.Error("Tool execution failed", "tool", displayName, "error", err)
		*steps = append(*steps, ChatStep{
			Type:   "tool_result",
			Name:   displayName,
			Result: "Error: " + err.Error(),
		})
		return sendToolResponse(ctx, cs, fc.Name, map[string]any{"error": err.Error()}, "failed to send tool response")
	}

	logger.Info("Tool executed successfully", "tool", displayName)
	*steps = append(*steps, ChatStep{
		Type:   "tool_result",
		Name:   displayName,
		Result: result,
	})

	return sendToolResponse(ctx, cs, fc.Name, toolResultResponsePayload(result), "failed to send tool response")
}

func sendToolResponse(
	ctx context.Context,
	cs *genai.Chat,
	functionName string,
	responsePayload map[string]any,
	wrapErrMessage string,
) (*genai.GenerateContentResponse, error) {
	resp, err := cs.SendMessage(ctx, genai.Part{
		FunctionResponse: &genai.FunctionResponse{
			Name:     functionName,
			Response: responsePayload,
		},
	})
	if err != nil {
		return nil, fmt.Errorf("%s: %w", wrapErrMessage, err)
	}
	return resp, nil
}

func toolResultResponsePayload(result string) map[string]any {
	var jsonResult map[string]any
	if err := json.Unmarshal([]byte(result), &jsonResult); err == nil {
		return jsonResult
	}
	return map[string]any{"result": result}
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
	w.Header().Set("Access-Control-Allow-Headers", "Content-Type")
}

func envOrDefault(key, fallback string) string {
	value := os.Getenv(key)
	if strings.TrimSpace(value) == "" {
		return fallback
	}
	return value
}

func normalizeToolCallName(name string) string {
	trimmed := strings.TrimSpace(name)
	if strings.Contains(trimmed, ":") {
		parts := strings.Split(trimmed, ":")
		return parts[len(parts)-1]
	}
	return trimmed
}
