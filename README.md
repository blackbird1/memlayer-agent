# Autonomize Interview ADK Wrapper

This project wraps the copied ADK server + web UI and adds local model tools for:

- GitHub: list pull requests, get issue details
- Jira: search issues via JQL, get issue details

## Homework Tool Coverage

Implemented ADK tools aligned to the assignment:

- `jira_get_assigned_issues`: assigned issues, statuses, and recent updates for a member
- `github_get_recent_commits`: recent commits by GitHub user
- `github_get_active_pull_requests`: active/open PRs by GitHub user
- `github_list_recent_contributed_repositories`: recently contributed repositories

Additional helper tools:

- `jira_search_issues`, `jira_get_issue`
- `github_list_pull_requests`, `github_get_issue`

## Environment

Copy `.env.example` to your runtime env file and set values:

- `GOOGLE_API_KEY` or `GEMINI_API_KEY` (required)
- `GITHUB_TOKEN` (required for GitHub tools)
- `JIRA_BASE_URL`, `JIRA_EMAIL`, `JIRA_API_TOKEN` (required for Jira tools)
- `REDIS_ADDR` (optional, defaults to `localhost:6379`)

## Run server locally

```bash
go run ./cmd/adk-server
```

Server starts on `:9090`.

## Run web frontend

Serve `web/index.html` with nginx (or any static server) and proxy `/api/` to `adk-server:9090` (see `web/nginx.conf`).

## Run with Docker Compose

1. Ensure `.env` exists (it can be a symlink to your selected environment file).
2. Start the stack:

```bash
docker compose up --build
```

3. Open `http://localhost:8080`.
