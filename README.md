# MemLayer Agent

A self-learning AI agent demo showcasing [MemLayer](https://prociq.ai) — persistent memory by ProcIQ. The agent follows a **Retrieve → Act → Log** memory cycle, accumulating knowledge across sessions and using it to inform future responses.

## What it does

- **Chat interface** — web UI for conversational interaction with the agent
- **Persistent memory** — integrates with MemLayer's MCP server to retrieve past context before acting and log outcomes after, so the agent learns over time
- **Example tool integration** — Finnhub stock market tools (quotes, news, earnings, analyst data) included as a reference implementation; enabled automatically when `FINNHUB_API_KEY` is set
- **Any LLM provider** — works with Gemini, OpenAI, Ollama, or any OpenAI-compatible endpoint via `OPENAI_BASE_URL`
- **MCP tool support** — connects to any MCP server for additional tool sets

## Stack

| Component | Role |
|-----------|------|
| Go (`adk-server`) | API server, LLM client, tool orchestration (default) |
| Python (`adk-server-python`) | Python implementation of the same API server |
| Redis | Chat session / history store |
| Nginx (`adk-web`) | Static UI + `/api/*` reverse proxy |
| LLM | Any OpenAI-compatible provider (default: Gemini) |
| MemLayer MCP | Persistent memory (episodes, notes, patterns, skills) |
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

See [`go/README.md`](./go/README.md) or [`python/README.md`](./python/README.md) for full setup, local dev, and troubleshooting.

## How it works

The agent follows a **Retrieve → Act → Log** memory cycle powered by MemLayer:

1. **Retrieve** — before acting, fetch relevant past episodes, patterns, and skills
2. **Act** — perform the task informed by retrieved context; on error, log and retrieve a known fix before retrying
3. **Log** — record what was done and learned for future sessions

See [`docs/how-it-works.md`](./docs/how-it-works.md) for the full architecture walkthrough.

## Repository layout

```
go/             Go implementation (adk-server)
python/         Python implementation (adk-server-python)
web/            Shared Nginx + chat UI
docs/           Architecture and design docs
.env.example    Environment variable reference
CONTRIBUTING.md How to contribute
```
