# Autonomize Interview ADK Wrapper

This project wraps the copied ADK server + web UI and adds local model tools for GitHub and Jira workflows.

## Homework Tool Coverage

Implemented ADK tools:

- `jira_get_assigned_issues`: assigned issues, statuses, and recent updates for a member
- `jira_create_issue`: create a Jira issue in a project
- `jira_assign_issue`: assign a Jira issue to a Jira accountId
- `jira_list_projects`: list Jira projects visible to the configured account
- `jira_validate_connection`: validate Jira authentication/project visibility
- `jira_search_issues`: search issues via JQL
- `jira_get_issue`: get Jira issue details by issue key
- `github_get_recent_commits`: recent commits by GitHub user
- `github_get_active_pull_requests`: active/open PRs by GitHub user
- `github_list_recent_contributed_repositories`: recently contributed repositories
- `github_list_pull_requests`: list pull requests for a repository
- `github_get_issue`: get GitHub issue details by issue number

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
