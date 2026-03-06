# Autonomize Interview ADK Wrapper

This project wraps the copied ADK server + web UI and adds local model tools for:

- GitHub: list pull requests, get issue details
- Jira: search issues via JQL, get issue details

## Environment

Copy `.env.example` to your runtime env file and set values:

- `GOOGLE_API_KEY` or `GEMINI_API_KEY` (required)
- `GITHUB_TOKEN` (required for GitHub tools)
- `JIRA_BASE_URL`, `JIRA_EMAIL`, `JIRA_API_TOKEN` (required for Jira tools)
- `REDIS_ADDR` (optional, defaults to `localhost:6379`)
- `MCP_URL`, `MCP_BEARER_TOKEN` (optional, enables passthrough MCP tools)

If MCP config is missing or unavailable, the app still runs with local GitHub/Jira tools.

## Run server locally

```bash
go run ./cmd/adk-server
```

Server starts on `:9090`.

## Run web frontend

Serve `web/index.html` with nginx (or any static server) and proxy `/api/` to `adk-server:9090` (see `web/nginx.conf`).
