# Python Implementation

FastAPI + `google-genai` implementation of the MemLayer Agent API server.

## Prerequisites

- Python 3.11+
- Redis 7+ (or Docker)
- A `.env` file at the repo root (see [`.env.example`](../.env.example))

## Step 1: Configure environment

```bash
cp ../.env.example ../.env
# fill in GOOGLE_API_KEY and MEMLAYER_MCP_BEARER_TOKEN
```

## Step 2A: Run with Docker (recommended)

From the repo root:

```bash
API_BACKEND=adk-server-python:9090 docker compose --profile python up --build
```

Services:
- `adk-web` — chat UI at `http://localhost:8080`
- `adk-server-python` — API server at `http://localhost:9091`
- `adk-redis` — session store at `localhost:6379`

## Step 2B: Run locally

Install dependencies:

```bash
cd python
pip install -e .
```

Start Redis (if not already running):

```bash
docker run --rm -p 6379:6379 redis:7-alpine
```

Run the server:

```bash
cd ..  # repo root
python -m memlayer_agent
```

Server starts on `:9090` by default. Override with `PORT=<n>`.

## Step 3: Smoke test

```bash
curl -sS -X POST http://localhost:9090/api/chat \
  -H "Content-Type: application/json" \
  -d '{"message":"hello","sessionId":"smoke-test"}'
```

Expected: JSON with a `response` field and no `error`.

## MCP configuration

By default the server reads `MCP_URL` from the environment and connects over Streamable HTTP.

For multi-server or stdio setups, create a `mcp_settings.json` (or `.mcp_settings.json`) at the working directory:

```json
{
  "mcpServers": {
    "prociq": {
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

**`GEMINI_API_KEY or GOOGLE_API_KEY is required`**
- Set `GOOGLE_API_KEY` in `.env` and restart.

**`Redis connected` not in logs / connection refused**
- Verify Redis is running: `redis-cli ping`
- Check `REDIS_ADDR` matches where Redis is listening.

**No logs appearing**
- Ensure `PYTHONUNBUFFERED=1` is set (already in `Dockerfile.python`; set it manually for local runs if needed).

**Port conflict on 9090**
- Set `PORT=<other>` before starting the server.
- In Docker Compose the Python server maps to host port `9091` to avoid clashing with the Go backend.
