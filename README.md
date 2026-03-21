# Autonomize Interview ADK Wrapper

Local ADK-style chat stack with:
- Go API server (`adk-server`) on `:9090`
- Redis session/history store on `:6379`
- Nginx web UI (`adk-web`) on `:8080` with `/api/*` proxy to `adk-server`

## Setup Pattern

Use this exact flow every time:
1. Prepare env file and secrets.
2. Start services (Docker recommended, local also supported).
3. Run smoke checks (API + UI).
4. Validate optional MCP integrations.
5. Troubleshoot from logs if checks fail.

## Prerequisites

Docker path:
- Docker Engine
- Docker Compose v2

Local path (without Docker for server process):
- Go `1.25.5` (matches [`go.mod`](/Users/stephenturner/src/AutonomizeInterview/go.mod))
- Redis `7+` reachable at `REDIS_ADDR` (default `localhost:6379`)

External credentials:
- Required: one of `GOOGLE_API_KEY` or `GEMINI_API_KEY`
- Optional: `MEMLAYER_MCP_BEARER_TOKEN` for MemLayer MCP over HTTP

## Step 1: Configure Environment

Create a local env file and point `.env` at it:

```bash
cp .env.example .env.local
ln -sf .env.local .env
```

Fill `.env.local`:

```bash
# Required (set one)
GOOGLE_API_KEY=your_google_or_gemini_key
# GEMINI_API_KEY=your_google_or_gemini_key

# Local server default (Docker compose overrides for adk-server)
REDIS_ADDR=localhost:6379

# Optional MemLayer MCP integration
MEMLAYER_MCP_BEARER_TOKEN=your_memlayer_mcp_bearer_token
```

If you configure MemLayer MCP in a settings file, the server also accepts a `bearerToken` or `bearer_token` field and converts it into an `Authorization: Bearer ...` header. An explicit `headers.Authorization` entry still wins if you prefer to set the header directly.

## Step 2A: Start with Docker (Recommended)

Build and run all services:

```bash
docker compose up --build
```

Expected services:
- `adk-web` on `http://localhost:8080`
- `adk-server` on `http://localhost:9090`
- `adk-redis` on `localhost:6379`

Check status:

```bash
docker compose ps
```

Stop stack:

```bash
docker compose down
```

## Step 2B: Start Server Locally (Alternative)

Use this when you want to run Go directly.

Download dependencies:

```bash
go mod download
```

Start Redis (example):

```bash
docker run --rm -p 6379:6379 redis:7-alpine
```

Start API server:

```bash
go run ./cmd/adk-server
```

Expected startup behavior:
- Logs include local tool registration count.
- Server starts listening on port `9090`.

For UI in local mode:
- Either run Docker Compose just for web/proxy, or
- Serve static UI yourself using [`web/index.html`](/Users/stephenturner/src/AutonomizeInterview/web/index.html) and proxy rules in [`web/nginx.conf`](/Users/stephenturner/src/AutonomizeInterview/web/nginx.conf)

## Step 3: Smoke Test

API smoke test:

```bash
curl -sS -X POST http://localhost:9090/api/chat \
  -H "Content-Type: application/json" \
  -d '{"message":"What is Stephen working on?","sessionId":"smoke-test"}'
```

Expected result:
- JSON response with at least `response` or `error`
- If tools are configured, `steps` may include tool call/result entries

UI smoke test:
1. Open `http://localhost:8080`
2. Send a test message
3. Confirm response is rendered and no proxy/network error appears in browser devtools

## Step 4: Validate Integrations (Optional)

MemLayer MCP:
- Confirm `MEMLAYER_MCP_BEARER_TOKEN` is set if your MCP server requires bearer auth
- Run a chat prompt that should trigger a MemLayer MCP tool

## Troubleshooting

`GEMINI_API_KEY or GOOGLE_API_KEY is required`
- Cause: neither API key is set.
- Fix: set one key in `.env.local`, then restart server/container.

`Failed to connect to Redis`
- Cause: Redis not running or unreachable address.
- Fix: verify Redis is up and `REDIS_ADDR` is correct.
- Note: in Docker Compose, `adk-server` uses `REDIS_ADDR=redis:6379`.

UI loads but chat fails
- Cause: API/proxy issue.
- Fix: inspect `adk-server` logs and confirm nginx `/api/` proxy target is `adk-server:9090` in Docker network.

Port conflict on `8080`, `9090`, or `6379`
- Cause: local process already bound to required port.
- Fix: stop conflicting process or remap port(s) in [`docker-compose.yml`](/Users/stephenturner/src/AutonomizeInterview/docker-compose.yml).
