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
	openai "github.com/sashabaranov/go-openai"
)

const (
	port            = "9090"
	sessionTTL      = 30 * time.Minute
	assistantPrompt = `You are a concise assistant augmented with a self-learning memory layer via ProcIQ MCP tools.

## Memory Cycle (Retrieve -> Act -> Log)

For every non-trivial task (coding, debugging, research, architecture):

1. RETRIEVE first: call prociq_retrieve_context with a clear task description before acting.
   - If the result contains Skills or Patterns, treat those as mandatory procedural guidance.
   - After the first retrieval, call prociq_list_scopes to resolve the default scope for this session.
   - If only one scope is available, use it. If multiple, pick the most relevant or ask the user once.

2. ACT: perform the task informed by retrieved context.
   - On any error: stop, call prociq_log_episode with outcome=failure, then call prociq_retrieve_context
     describing the error before retrying.
   - For static facts worth preserving, call prociq_log_note.

3. LOG: you MUST call prociq_log_episode as a tool call BEFORE giving your final text response.
   - This is a required tool call, not optional. Do not skip it.
   - Required fields: task_goal, approach_taken, outcome (success/partial/failure), scope.
   - Skip only for trivial or purely conversational exchanges (e.g. "hello", "thanks").

## General Behaviour
Use available MCP tools when they help answer the user's question.
Keep responses short, structured, and grounded in tool results when tools are used.
Always produce a final text response to the user after completing tool calls — never end a turn with only tool calls and no message.`
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

var (
	logger      *slog.Logger
	redisClient *redis.Client
	mcpManager  *MCPServerManager
	llmClient   *openai.Client
	llmModel    string
)

func init() {
	logger = slog.New(slog.NewJSONHandler(os.Stdout, nil))
}

// resolveClient builds the OpenAI-compatible client from environment variables.
// Supports Gemini, OpenAI, Ollama, and any other OpenAI-protocol provider.
func resolveClient() (*openai.Client, string, error) {
	openAIKey := strings.TrimSpace(os.Getenv("OPENAI_API_KEY"))
	googleKey := firstNonEmpty(
		strings.TrimSpace(os.Getenv("GOOGLE_API_KEY")),
		strings.TrimSpace(os.Getenv("GEMINI_API_KEY")),
	)
	baseURL := strings.TrimSpace(os.Getenv("OPENAI_BASE_URL"))
	model := strings.TrimSpace(os.Getenv("MODEL"))

	var apiKey string
	if openAIKey != "" {
		apiKey = openAIKey
		if model == "" {
			model = "gpt-4o-mini"
		}
	} else if googleKey != "" {
		apiKey = googleKey
		if baseURL == "" {
			baseURL = "https://generativelanguage.googleapis.com/v1beta/openai/"
		}
		if model == "" {
			model = "gemini-2.0-flash"
		}
	} else {
		return nil, "", fmt.Errorf("OPENAI_API_KEY or GOOGLE_API_KEY is required")
	}

	cfg := openai.DefaultConfig(apiKey)
	if baseURL != "" {
		cfg.BaseURL = baseURL
	}
	return openai.NewClientWithConfig(cfg), model, nil
}

func main() {
	redisAddr := envOrDefault("REDIS_ADDR", "localhost:6379")
	redisClient = redis.NewClient(&redis.Options{Addr: redisAddr})
	if err := redisClient.Ping(context.Background()).Err(); err != nil {
		logger.Error("Failed to connect to Redis", "error", err)
	}

	var err error
	llmClient, llmModel, err = resolveClient()
	if err != nil {
		logger.Error(err.Error())
		os.Exit(1)
	}
	logger.Info("LLM client configured", "model", llmModel)

	mcpManager = NewMCPServerManager(logger)
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
			http.Error(w, "Invalid request body", http.StatusBadRequest)
			return
		}
		if req.SessionID == "" {
			req.SessionID = "default"
		}

		logger.Info("Received chat request", "sessionId", req.SessionID)

		steps, resp, err := handleChat(r.Context(), req.SessionID, req.Message)
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

func handleChat(ctx context.Context, sessionID, message string) ([]ChatStep, string, error) {
	startedAt := time.Now()

	// Build tool list and executors.
	var tools []openai.Tool
	toolExecutors := make(map[string]ToolExecutor)

	if mcpManager != nil {
		mcpTools, mcpExecutors, err := mcpManager.ListAllTools(ctx)
		if err != nil {
			logger.Error("Failed to list MCP tools", "sessionId", sessionID, "error", err)
		} else {
			tools = append(tools, mcpTools...)
			for k, v := range mcpExecutors {
				toolExecutors[k] = v
			}
			logger.Info("MCP tools registered", "sessionId", sessionID, "count", len(mcpTools))
		}
	}

	if os.Getenv("FINNHUB_API_KEY") != "" {
		finnhubTools, finnhubExecutors := buildFinnhubToolset()
		tools = append(tools, finnhubTools...)
		for k, v := range finnhubExecutors {
			toolExecutors[k] = v
		}
		logger.Info("Finnhub example tools registered", "sessionId", sessionID, "count", len(finnhubTools))
	}

	// Load history and build message list.
	history, err := loadHistory(ctx, sessionID)
	if err != nil {
		logger.Error("Failed to load history", "sessionId", sessionID, "error", err)
	}

	messages := []openai.ChatCompletionMessage{
		{Role: openai.ChatMessageRoleSystem, Content: assistantPrompt},
	}
	messages = append(messages, history...)
	messages = append(messages, openai.ChatCompletionMessage{
		Role:    openai.ChatMessageRoleUser,
		Content: message,
	})

	var steps []ChatStep
	var finalText string

	// Tool call loop.
	for {
		req := openai.ChatCompletionRequest{
			Model:    llmModel,
			Messages: messages,
		}
		if len(tools) > 0 {
			req.Tools = tools
		}

		resp, err := llmClient.CreateChatCompletion(ctx, req)
		if err != nil {
			return nil, "", fmt.Errorf("LLM request failed: %w", err)
		}
		if len(resp.Choices) == 0 {
			break
		}

		choice := resp.Choices[0]
		messages = append(messages, choice.Message)

		if len(choice.Message.ToolCalls) == 0 {
			finalText = choice.Message.Content
			break
		}

		for _, tc := range choice.Message.ToolCalls {
			toolName := normalizeToolCallName(tc.Function.Name)
			var args map[string]any
			json.Unmarshal([]byte(tc.Function.Arguments), &args)

			logger.Info("Tool call received", "tool", toolName, "args", args)
			steps = append(steps, ChatStep{Type: "tool_call", Name: toolName, Args: args})

			var resultStr string
			executor, ok := toolExecutors[toolName]
			if !ok {
				errMsg := fmt.Sprintf("tool %q is not available", toolName)
				resultStr = `{"error":"` + errMsg + `"}`
				steps = append(steps, ChatStep{Type: "tool_result", Name: toolName, Result: "Error: " + errMsg})
			} else {
				result, execErr := executor(ctx, args)
				if execErr != nil {
					logger.Error("Tool execution failed", "tool", toolName, "error", execErr)
					resultStr = `{"error":"` + execErr.Error() + `"}`
					steps = append(steps, ChatStep{Type: "tool_result", Name: toolName, Result: "Error: " + execErr.Error()})
				} else {
					logger.Info("Tool executed successfully", "tool", toolName)
					resultStr = result
					steps = append(steps, ChatStep{Type: "tool_result", Name: toolName, Result: result})
				}
			}

			messages = append(messages, openai.ChatCompletionMessage{
				Role:       openai.ChatMessageRoleTool,
				ToolCallID: tc.ID,
				Content:    resultStr,
			})
		}
	}

	// Save history (skip system message).
	if err := saveHistory(ctx, sessionID, messages[1:]); err != nil {
		logger.Error("Failed to save history", "sessionId", sessionID, "error", err)
	}

	logger.Info("handleChat completed", "sessionId", sessionID, "steps", len(steps),
		"response_chars", len(strings.TrimSpace(finalText)), "duration_ms", time.Since(startedAt).Milliseconds())
	return steps, finalText, nil
}

func saveHistory(ctx context.Context, sessionID string, messages []openai.ChatCompletionMessage) error {
	data, err := json.Marshal(messages)
	if err != nil {
		return err
	}
	return redisClient.Set(ctx, "session:"+sessionID, data, sessionTTL).Err()
}

func loadHistory(ctx context.Context, sessionID string) ([]openai.ChatCompletionMessage, error) {
	data, err := redisClient.Get(ctx, "session:"+sessionID).Bytes()
	if err != nil {
		if err == redis.Nil {
			return nil, nil
		}
		return nil, err
	}
	var messages []openai.ChatCompletionMessage
	if err := json.Unmarshal(data, &messages); err != nil {
		// Stale history from old format — start fresh.
		logger.Warn("Discarding unreadable session history", "sessionId", sessionID)
		return nil, nil
	}
	return messages, nil
}

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
