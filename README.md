# MemLayer Agent

A self-learning AI agent demo built on Google Gemini with persistent memory via [ProcIQ](https://prociq.ai). The agent follows a **Retrieve → Act → Log** memory cycle, accumulating knowledge across sessions and using it to inform future responses.

## What it does

- **Chat interface** — web UI for conversational interaction with the agent
- **Persistent memory** — integrates with ProcIQ's MCP server to retrieve past context before acting and log outcomes after, so the agent learns over time
- **Example tool integration** — Finnhub stock market tools (quotes, news, earnings, analyst data) included as a reference implementation; enabled automatically when `FINNHUB_API_KEY` is set
- **Configurable model** — swap Gemini models via `MODEL` env var (Flash, Pro, Deep Think)
- **MCP tool support** — connects to any MCP server for additional tool sets

## Stack

| Component | Role |
|-----------|------|
| Go (`adk-server`) | API server, Gemini client, tool orchestration (default) |
| Python (`adk-server-python`) | Python implementation of the same API server |
| Redis | Chat session / history store |
| Nginx (`adk-web`) | Static UI + `/api/*` reverse proxy |
| Gemini API | LLM (default: `gemini-3-flash-preview`) |
| ProcIQ MCP | Persistent memory (episodes, notes, patterns) |
| Finnhub API | Example tool integration (optional) — live stock market data |

## Quick start

```bash
cp .env.example .env
# fill in GOOGLE_API_KEY and MEMLAYER_MCP_BEARER_TOKEN
docker compose --profile go up --build
```

Open `http://localhost:8080`.

## Switching backends

Use Docker Compose profiles to choose between the Go and Python implementations:

```bash
# Go (default)
docker compose --profile go up --build

# Python
API_BACKEND=adk-server-python:9090 docker compose --profile python up --build
```

The `API_BACKEND` env var tells the Nginx proxy which backend container to route `/api/*` traffic to.

See [`go/README.md`](./go/README.md) for full setup, local dev, and troubleshooting.

## Repository layout

```
go/          Go implementation (adk-server)
python/      Python implementation (adk-server-python)
web/         Shared Nginx + chat UI
.env.example Environment variable reference
```
