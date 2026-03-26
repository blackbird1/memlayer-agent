# Go Implementation

Go API server (`adk-server`) for the MemLayer Agent demo.

## Prerequisites

- Go 1.22+ (for local dev without Docker)
- Redis 7+ (or Docker)
- A `.env` file at the repo root (see [`.env.example`](../.env.example))

## Step 1: Configure environment

```bash
cp ../.env.example ../.env
# fill in GOOGLE_API_KEY (or OPENAI_API_KEY) and MEMLAYER_MCP_BEARER_TOKEN
```

## Step 2A: Run with Docker (recommended)

From the repo root:

```bash
docker compose --profile go up --build
```

Services:
- `adk-web` — chat UI at `http://localhost:8080`
- `adk-server` — API server at `http://localhost:9090`
- `adk-redis` — session store at `localhost:6379`

## Step 2B: Run locally

Start Redis (if not already running):

```bash
docker run --rm -p 6379:6379 redis:7-alpine
```

Run the server (from the repo root):

```bash
go run ./go/cmd/adk-server
```

Server starts on `:9090` by default.

## Step 3: Smoke test

```bash
curl -sS -X POST http://localhost:9090/api/chat \
  -H "Content-Type: application/json" \
  -d '{"message":"hello","sessionId":"smoke-test"}'
```

Expected: JSON with a `response` field and no `error`.

## LLM provider

The server auto-detects the provider from environment variables:

| Env var | Provider | Default model |
|---------|----------|---------------|
| `GOOGLE_API_KEY` | Gemini (via OpenAI-compatible endpoint) | `gemini-2.0-flash` |
| `OPENAI_API_KEY` | OpenAI | `gpt-4o-mini` |
| `OPENAI_API_KEY` + `OPENAI_BASE_URL` | Any OpenAI-compatible (Ollama, Groq, etc.) | set via `MODEL` |

Override the model with `MODEL=<model-id>`.

## MCP configuration

By default the server reads `MCP_URL` from the environment and connects over Streamable HTTP.

For multi-server or stdio setups, create a `mcp_settings.json` (or `.mcp_settings.json`) at the working directory:

```json
{
  "mcpServers": {
    "memlayer": {
      "url": "https://your-mcp-host/mcp",
      "bearerToken": "your_token"
    },
    "local-tool": {
      "command": "npx",
      "args": ["-y", "@some/mcp-server"]
    }
  }
}
```

The server also checks `.gemini/settings.json` for compatibility with the Gemini CLI.

## Troubleshooting

**`OPENAI_API_KEY or GOOGLE_API_KEY is required`**
- Set `GOOGLE_API_KEY` (or `OPENAI_API_KEY`) in `.env` and restart.

**`Failed to connect to Redis`**
- Verify Redis is running: `redis-cli ping`
- Check `REDIS_ADDR` matches where Redis is listening.

**Port conflict on `8080`, `9090`, or `6379`**
- Stop the conflicting process or remap ports in `docker-compose.yml`.
