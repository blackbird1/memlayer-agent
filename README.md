# MemLayer Agent

A self-learning AI agent demo built on Google Gemini with persistent memory via [ProcIQ](https://prociq.ai). The agent follows a **Retrieve → Act → Log** memory cycle, accumulating knowledge across sessions and using it to inform future responses.

## What it does

- **Chat interface** — web UI for conversational interaction with the agent
- **Persistent memory** — integrates with ProcIQ's MCP server to retrieve past context before acting and log outcomes after, so the agent learns over time
- **Stock market tools** — built-in Finnhub tools for real-time quotes, company profiles, news, earnings, analyst recommendations, insider sentiment, and price targets
- **Configurable model** — swap Gemini models via `MODEL` env var (Flash, Pro, Deep Think)
- **MCP tool support** — connects to any MCP server for additional tool sets

## Stack

| Component | Role |
|-----------|------|
| Go (`adk-server`) | API server, Gemini client, tool orchestration |
| Redis | Chat session / history store |
| Nginx (`adk-web`) | Static UI + `/api/*` reverse proxy |
| Gemini API | LLM (default: `gemini-3-flash-preview`) |
| ProcIQ MCP | Persistent memory (episodes, notes, patterns) |
| Finnhub API | Live stock market data |

## Quick start

```bash
cp .env.example .env
# fill in GOOGLE_API_KEY, FINNHUB_API_KEY, MEMLAYER_MCP_BEARER_TOKEN
docker compose up --build
```

Open `http://localhost:8080`.

See [`go/README.md`](./go/README.md) for full setup, local dev, and troubleshooting.

## Repository layout

```
go/          Go implementation (adk-server)
python/      Python scaffold (unused)
web/         Shared Nginx + chat UI
.env.example Environment variable reference
```
