package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"google.golang.org/genai"
)

type localTools struct {
	github *GitHubService
	jira   *JiraService
	logger *slog.Logger
}

func listLocalToolNames() []string {
	return []string{
		"jira_get_assigned_issues",
		"jira_create_issue",
		"jira_assign_issue",
		"jira_list_projects",
		"jira_validate_connection",
		"github_get_recent_commits",
		"github_get_active_pull_requests",
		"github_list_recent_contributed_repositories",
		"github_list_pull_requests",
		"github_get_issue",
		"jira_search_issues",
		"jira_get_issue",
	}
}

func buildLocalToolset() ([]*genai.FunctionDeclaration, map[string]ToolExecutor) {
	httpClient := &http.Client{Timeout: 20 * time.Second}
	toolLogger := logger.With("component", "local_tools")
	toolset := &localTools{
		github: NewGitHubServiceFromEnv(httpClient, logger.With("component", "github_service")),
		jira:   NewJiraServiceFromEnv(httpClient, logger.With("component", "jira_service")),
		logger: toolLogger,
	}

	declarations := []*genai.FunctionDeclaration{
		{
			Name:        "jira_get_assigned_issues",
			Description: "Get Jira issues assigned to a team member with status and recent update timestamps.",
			Parameters: schemaObject(map[string]*genai.Schema{
				"assignee":            schemaString("Assignee identifier for Jira JQL, e.g. email, accountId, or display name depending on your Jira setup."),
				"max_results":         schemaInteger("Maximum number of issues to return (1-100). Defaults to 20."),
				"updated_within_days": schemaInteger("Optional filter to only include issues updated in the last N days."),
			}, []string{"assignee"}),
		},
		{
			Name:        "jira_create_issue",
			Description: "Create a Jira issue in a project.",
			Parameters: schemaObject(map[string]*genai.Schema{
				"project_key":   schemaString("Jira project key, e.g. ENG."),
				"summary":       schemaString("Issue summary/title."),
				"description":   schemaString("Issue description in plain text."),
				"issue_type":    schemaString("Issue type name, e.g. Task, Bug, Story. Defaults to Task."),
				"assignee_id":   schemaString("Optional Jira accountId to assign issue to."),
				"priority_name": schemaString("Optional priority name, e.g. High."),
			}, []string{"project_key", "summary"}),
		},
		{
			Name:        "jira_assign_issue",
			Description: "Assign an existing Jira issue to a Jira user accountId.",
			Parameters: schemaObject(map[string]*genai.Schema{
				"issue_key":   schemaString("Jira issue key, e.g. ENG-123."),
				"assignee_id": schemaString("Jira accountId of the assignee."),
			}, []string{"issue_key", "assignee_id"}),
		},
		{
			Name:        "jira_list_projects",
			Description: "List Jira projects visible to the configured Jira account.",
			Parameters: schemaObject(map[string]*genai.Schema{
				"max_results": schemaInteger("Maximum number of projects to return (1-200). Defaults to 50."),
			}, nil),
		},
		{
			Name:        "jira_validate_connection",
			Description: "Validate Jira authentication, visible projects, and project-level permissions for creating issues.",
			Parameters: schemaObject(map[string]*genai.Schema{
				"project_key": schemaString("Optional Jira project key to validate, e.g. ENG."),
				"max_results": schemaInteger("Maximum number of visible projects to return (1-200). Defaults to 20."),
			}, nil),
		},
		{
			Name:        "github_get_recent_commits",
			Description: "Get recent commits made by a GitHub user from recent push activity.",
			Parameters: schemaObject(map[string]*genai.Schema{
				"username":    schemaString("GitHub username."),
				"since_days":  schemaInteger("How many days back to search. Defaults to 7."),
				"max_results": schemaInteger("Maximum number of commits to return (1-200). Defaults to 30."),
			}, []string{"username"}),
		},
		{
			Name:        "github_get_active_pull_requests",
			Description: "Get open pull requests authored by a GitHub user.",
			Parameters: schemaObject(map[string]*genai.Schema{
				"username":    schemaString("GitHub username."),
				"max_results": schemaInteger("Maximum number of pull requests to return (1-100). Defaults to 20."),
			}, []string{"username"}),
		},
		{
			Name:        "github_list_recent_contributed_repositories",
			Description: "List repositories a GitHub user has recently contributed to based on activity events.",
			Parameters: schemaObject(map[string]*genai.Schema{
				"username":    schemaString("GitHub username."),
				"since_days":  schemaInteger("How many days back to search. Defaults to 30."),
				"max_results": schemaInteger("Maximum repositories to return (1-100). Defaults to 20."),
			}, []string{"username"}),
		},
		{
			Name:        "github_list_pull_requests",
			Description: "List pull requests from a GitHub repository.",
			Parameters: schemaObject(map[string]*genai.Schema{
				"owner":    schemaString("Repository owner, e.g. octocat."),
				"repo":     schemaString("Repository name, e.g. hello-world."),
				"state":    schemaString("PR state: open, closed, or all. Defaults to open."),
				"per_page": schemaInteger("Number of PRs to return (1-100). Defaults to 20."),
			}, []string{"owner", "repo"}),
		},
		{
			Name:        "github_get_issue",
			Description: "Fetch a single GitHub issue by number.",
			Parameters: schemaObject(map[string]*genai.Schema{
				"owner":  schemaString("Repository owner."),
				"repo":   schemaString("Repository name."),
				"number": schemaInteger("Issue number."),
			}, []string{"owner", "repo", "number"}),
		},
		{
			Name:        "jira_search_issues",
			Description: "Search Jira issues using JQL.",
			Parameters: schemaObject(map[string]*genai.Schema{
				"jql":         schemaString("Jira Query Language string."),
				"max_results": schemaInteger("Maximum number of issues to return (1-100). Defaults to 20."),
			}, []string{"jql"}),
		},
		{
			Name:        "jira_get_issue",
			Description: "Fetch details for a Jira issue key (e.g. PROJ-123).",
			Parameters: schemaObject(map[string]*genai.Schema{
				"issue_key": schemaString("Jira issue key."),
			}, []string{"issue_key"}),
		},
	}

	executors := map[string]ToolExecutor{
		"jira_get_assigned_issues":                    toolset.jiraGetAssignedIssues,
		"jira_create_issue":                           toolset.jiraCreateIssue,
		"jira_assign_issue":                           toolset.jiraAssignIssue,
		"jira_list_projects":                          toolset.jiraListProjects,
		"jira_validate_connection":                    toolset.jiraValidateConnection,
		"github_get_recent_commits":                   toolset.githubGetRecentCommits,
		"github_get_active_pull_requests":             toolset.githubGetActivePullRequests,
		"github_list_recent_contributed_repositories": toolset.githubListRecentContributedRepositories,
		"github_list_pull_requests":                   toolset.githubListPullRequests,
		"github_get_issue":                            toolset.githubGetIssue,
		"jira_search_issues":                          toolset.jiraSearchIssues,
		"jira_get_issue":                              toolset.jiraGetIssue,
	}

	return declarations, executors
}

func (t *localTools) jiraGetAssignedIssues(ctx context.Context, args map[string]any) (string, error) {
	assignee, err := getRequiredStringArg(args, "assignee")
	if err != nil {
		return "", err
	}
	maxResults, err := getOptionalIntArg(args, "max_results", 20, 1, 100)
	if err != nil {
		return "", err
	}
	updatedWithinDays, err := getOptionalIntArg(args, "updated_within_days", 0, 0, 3650)
	if err != nil {
		return "", err
	}

	t.logger.Info("Tool execution started", "tool", "jira_get_assigned_issues", "assignee", assignee, "max_results", maxResults, "updated_within_days", updatedWithinDays)
	result, err := t.jira.GetAssignedIssues(ctx, assignee, maxResults, updatedWithinDays)
	if err != nil {
		t.logger.Error("Tool execution failed", "tool", "jira_get_assigned_issues", "assignee", assignee, "error", err)
		return "", err
	}
	t.logger.Info("Tool execution finished", "tool", "jira_get_assigned_issues", "assignee", assignee, "count", result.Count)
	return marshalJSON(result)
}

func (t *localTools) jiraCreateIssue(ctx context.Context, args map[string]any) (string, error) {
	projectKey, err := getRequiredStringArg(args, "project_key")
	if err != nil {
		return "", err
	}
	summary, err := getRequiredStringArg(args, "summary")
	if err != nil {
		return "", err
	}
	description, err := getOptionalStringArg(args, "description", "")
	if err != nil {
		return "", err
	}
	issueType, err := getOptionalStringArg(args, "issue_type", "Task")
	if err != nil {
		return "", err
	}
	assigneeID, err := getOptionalStringArg(args, "assignee_id", "")
	if err != nil {
		return "", err
	}
	priorityName, err := getOptionalStringArg(args, "priority_name", "")
	if err != nil {
		return "", err
	}

	t.logger.Info("Tool execution started", "tool", "jira_create_issue", "project_key", projectKey, "issue_type", issueType)
	result, err := t.jira.CreateIssue(ctx, projectKey, summary, description, issueType, assigneeID, priorityName)
	if err != nil {
		t.logger.Error("Tool execution failed", "tool", "jira_create_issue", "project_key", projectKey, "error", err)
		return "", err
	}
	t.logger.Info("Tool execution finished", "tool", "jira_create_issue", "project_key", projectKey, "key", result.Key)
	return marshalJSON(result)
}

func (t *localTools) jiraAssignIssue(ctx context.Context, args map[string]any) (string, error) {
	issueKey, err := getRequiredStringArg(args, "issue_key")
	if err != nil {
		return "", err
	}
	assigneeID, err := getRequiredStringArg(args, "assignee_id")
	if err != nil {
		return "", err
	}

	t.logger.Info("Tool execution started", "tool", "jira_assign_issue", "issue_key", issueKey)
	result, err := t.jira.AssignIssue(ctx, issueKey, assigneeID)
	if err != nil {
		t.logger.Error("Tool execution failed", "tool", "jira_assign_issue", "issue_key", issueKey, "error", err)
		return "", err
	}
	t.logger.Info("Tool execution finished", "tool", "jira_assign_issue", "issue_key", issueKey, "assignee_id", assigneeID)
	return marshalJSON(result)
}

func (t *localTools) jiraListProjects(ctx context.Context, args map[string]any) (string, error) {
	maxResults, err := getOptionalIntArg(args, "max_results", 50, 1, 200)
	if err != nil {
		return "", err
	}

	t.logger.Info("Tool execution started", "tool", "jira_list_projects", "max_results", maxResults)
	result, err := t.jira.ListProjects(ctx, maxResults)
	if err != nil {
		t.logger.Error("Tool execution failed", "tool", "jira_list_projects", "max_results", maxResults, "error", err)
		return "", err
	}
	t.logger.Info("Tool execution finished", "tool", "jira_list_projects", "count", result.Count)
	return marshalJSON(result)
}

func (t *localTools) jiraValidateConnection(ctx context.Context, args map[string]any) (string, error) {
	projectKey, err := getOptionalStringArg(args, "project_key", "")
	if err != nil {
		return "", err
	}
	maxResults, err := getOptionalIntArg(args, "max_results", 20, 1, 200)
	if err != nil {
		return "", err
	}

	t.logger.Info("Tool execution started", "tool", "jira_validate_connection", "project_key", projectKey, "max_results", maxResults)
	result, err := t.jira.ValidateConnection(ctx, projectKey, maxResults)
	if err != nil {
		t.logger.Error("Tool execution failed", "tool", "jira_validate_connection", "project_key", projectKey, "error", err)
		return "", err
	}
	t.logger.Info("Tool execution finished", "tool", "jira_validate_connection", "project_key", projectKey, "authenticated", result.Authenticated, "visible_project_count", result.VisibleProjectCount)
	return marshalJSON(result)
}

func (t *localTools) githubGetRecentCommits(ctx context.Context, args map[string]any) (string, error) {
	username, err := getRequiredStringArg(args, "username")
	if err != nil {
		return "", err
	}
	sinceDays, err := getOptionalIntArg(args, "since_days", 7, 1, 365)
	if err != nil {
		return "", err
	}
	maxResults, err := getOptionalIntArg(args, "max_results", 30, 1, 200)
	if err != nil {
		return "", err
	}

	t.logger.Info("Tool execution started", "tool", "github_get_recent_commits", "username", username, "since_days", sinceDays, "max_results", maxResults)
	result, err := t.github.GetRecentCommits(ctx, username, sinceDays, maxResults)
	if err != nil {
		t.logger.Error("Tool execution failed", "tool", "github_get_recent_commits", "username", username, "error", err)
		return "", err
	}
	t.logger.Info("Tool execution finished", "tool", "github_get_recent_commits", "username", username, "count", result.Count)
	return marshalJSON(result)
}

func (t *localTools) githubGetActivePullRequests(ctx context.Context, args map[string]any) (string, error) {
	username, err := getRequiredStringArg(args, "username")
	if err != nil {
		return "", err
	}
	maxResults, err := getOptionalIntArg(args, "max_results", 20, 1, 100)
	if err != nil {
		return "", err
	}

	t.logger.Info("Tool execution started", "tool", "github_get_active_pull_requests", "username", username, "max_results", maxResults)
	result, err := t.github.GetActivePullRequests(ctx, username, maxResults)
	if err != nil {
		t.logger.Error("Tool execution failed", "tool", "github_get_active_pull_requests", "username", username, "error", err)
		return "", err
	}
	t.logger.Info("Tool execution finished", "tool", "github_get_active_pull_requests", "username", username, "count", result.Count)
	return marshalJSON(result)
}

func (t *localTools) githubListRecentContributedRepositories(ctx context.Context, args map[string]any) (string, error) {
	username, err := getRequiredStringArg(args, "username")
	if err != nil {
		return "", err
	}
	sinceDays, err := getOptionalIntArg(args, "since_days", 30, 1, 365)
	if err != nil {
		return "", err
	}
	maxResults, err := getOptionalIntArg(args, "max_results", 20, 1, 100)
	if err != nil {
		return "", err
	}

	t.logger.Info("Tool execution started", "tool", "github_list_recent_contributed_repositories", "username", username, "since_days", sinceDays, "max_results", maxResults)
	result, err := t.github.ListRecentContributedRepositories(ctx, username, sinceDays, maxResults)
	if err != nil {
		t.logger.Error("Tool execution failed", "tool", "github_list_recent_contributed_repositories", "username", username, "error", err)
		return "", err
	}
	t.logger.Info("Tool execution finished", "tool", "github_list_recent_contributed_repositories", "username", username, "count", result.Count)
	return marshalJSON(result)
}

func (t *localTools) githubListPullRequests(ctx context.Context, args map[string]any) (string, error) {
	owner, err := getRequiredStringArg(args, "owner")
	if err != nil {
		return "", err
	}
	repo, err := getRequiredStringArg(args, "repo")
	if err != nil {
		return "", err
	}
	state, err := getOptionalStringArg(args, "state", "open")
	if err != nil {
		return "", err
	}
	perPage, err := getOptionalIntArg(args, "per_page", 20, 1, 100)
	if err != nil {
		return "", err
	}

	t.logger.Info("Tool execution started", "tool", "github_list_pull_requests", "owner", owner, "repo", repo, "state", state, "per_page", perPage)
	result, err := t.github.ListPullRequests(ctx, owner, repo, state, perPage)
	if err != nil {
		t.logger.Error("Tool execution failed", "tool", "github_list_pull_requests", "owner", owner, "repo", repo, "error", err)
		return "", err
	}
	t.logger.Info("Tool execution finished", "tool", "github_list_pull_requests", "owner", owner, "repo", repo, "count", result["count"])
	return marshalJSON(result)
}

func (t *localTools) githubGetIssue(ctx context.Context, args map[string]any) (string, error) {
	owner, err := getRequiredStringArg(args, "owner")
	if err != nil {
		return "", err
	}
	repo, err := getRequiredStringArg(args, "repo")
	if err != nil {
		return "", err
	}
	number, err := getRequiredIntArg(args, "number")
	if err != nil {
		return "", err
	}

	t.logger.Info("Tool execution started", "tool", "github_get_issue", "owner", owner, "repo", repo, "number", number)
	result, err := t.github.GetIssue(ctx, owner, repo, number)
	if err != nil {
		t.logger.Error("Tool execution failed", "tool", "github_get_issue", "owner", owner, "repo", repo, "number", number, "error", err)
		return "", err
	}
	t.logger.Info("Tool execution finished", "tool", "github_get_issue", "owner", owner, "repo", repo, "number", number)
	return marshalJSON(result)
}

func (t *localTools) jiraSearchIssues(ctx context.Context, args map[string]any) (string, error) {
	jql, err := getRequiredStringArg(args, "jql")
	if err != nil {
		return "", err
	}
	maxResults, err := getOptionalIntArg(args, "max_results", 20, 1, 100)
	if err != nil {
		return "", err
	}

	t.logger.Info("Tool execution started", "tool", "jira_search_issues", "max_results", maxResults)
	result, err := t.jira.SearchIssues(ctx, jql, maxResults)
	if err != nil {
		t.logger.Error("Tool execution failed", "tool", "jira_search_issues", "error", err)
		return "", err
	}
	t.logger.Info("Tool execution finished", "tool", "jira_search_issues", "count", result.Count)
	return marshalJSON(result)
}

func (t *localTools) jiraGetIssue(ctx context.Context, args map[string]any) (string, error) {
	issueKey, err := getRequiredStringArg(args, "issue_key")
	if err != nil {
		return "", err
	}

	t.logger.Info("Tool execution started", "tool", "jira_get_issue", "issue_key", issueKey)
	result, err := t.jira.GetIssue(ctx, issueKey)
	if err != nil {
		t.logger.Error("Tool execution failed", "tool", "jira_get_issue", "issue_key", issueKey, "error", err)
		return "", err
	}
	t.logger.Info("Tool execution finished", "tool", "jira_get_issue", "issue_key", issueKey)
	return marshalJSON(result)
}

func getRequiredStringArg(args map[string]any, key string) (string, error) {
	value, ok := args[key]
	if !ok {
		return "", fmt.Errorf("missing required argument: %s", key)
	}
	asString := strings.TrimSpace(fmt.Sprintf("%v", value))
	if asString == "" {
		return "", fmt.Errorf("argument %s must not be empty", key)
	}
	return asString, nil
}

func getOptionalStringArg(args map[string]any, key, fallback string) (string, error) {
	value, ok := args[key]
	if !ok {
		return fallback, nil
	}
	asString := strings.TrimSpace(fmt.Sprintf("%v", value))
	if asString == "" {
		return fallback, nil
	}
	return asString, nil
}

func getRequiredIntArg(args map[string]any, key string) (int, error) {
	value, ok := args[key]
	if !ok {
		return 0, fmt.Errorf("missing required argument: %s", key)
	}
	parsed, err := parseIntArg(value)
	if err != nil {
		return 0, fmt.Errorf("invalid integer argument %s: %w", key, err)
	}
	return parsed, nil
}

func getOptionalIntArg(args map[string]any, key string, fallback, min, max int) (int, error) {
	value, ok := args[key]
	if !ok {
		return fallback, nil
	}
	parsed, err := parseIntArg(value)
	if err != nil {
		return 0, fmt.Errorf("invalid integer argument %s: %w", key, err)
	}
	if parsed < min || parsed > max {
		return 0, fmt.Errorf("argument %s must be between %d and %d", key, min, max)
	}
	return parsed, nil
}

func parseIntArg(raw any) (int, error) {
	switch value := raw.(type) {
	case float64:
		return int(value), nil
	case int:
		return value, nil
	case int64:
		return int(value), nil
	case string:
		parsed, err := strconv.Atoi(strings.TrimSpace(value))
		if err != nil {
			return 0, err
		}
		return parsed, nil
	default:
		return 0, fmt.Errorf("unsupported type %T", raw)
	}
}

func schemaObject(properties map[string]*genai.Schema, required []string) *genai.Schema {
	return &genai.Schema{
		Type:       genai.TypeObject,
		Properties: properties,
		Required:   required,
	}
}

func schemaString(description string) *genai.Schema {
	return &genai.Schema{
		Type:        genai.TypeString,
		Description: description,
	}
}

func schemaInteger(description string) *genai.Schema {
	return &genai.Schema{
		Type:        genai.TypeInteger,
		Description: description,
	}
}

func marshalJSON(value any) (string, error) {
	raw, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return "", fmt.Errorf("marshal response: %w", err)
	}
	return string(raw), nil
}
