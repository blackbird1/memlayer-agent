package main

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"google.golang.org/genai"
)

const (
	defaultGitHubAPIBaseURL = "https://api.github.com"
	defaultJiraAPIPath      = "/rest/api/3"
)

type localTools struct {
	httpClient *http.Client
}

func buildLocalToolset() ([]*genai.FunctionDeclaration, map[string]ToolExecutor) {
	toolset := &localTools{
		httpClient: &http.Client{Timeout: 20 * time.Second},
	}

	declarations := []*genai.FunctionDeclaration{
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
		"github_list_pull_requests": toolset.githubListPullRequests,
		"github_get_issue":          toolset.githubGetIssue,
		"jira_search_issues":        toolset.jiraSearchIssues,
		"jira_get_issue":            toolset.jiraGetIssue,
	}

	return declarations, executors
}

func (t *localTools) githubListPullRequests(ctx context.Context, args map[string]any) (string, error) {
	token := strings.TrimSpace(os.Getenv("GITHUB_TOKEN"))
	if token == "" {
		return "", fmt.Errorf("GITHUB_TOKEN is not set")
	}

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

	baseURL := strings.TrimRight(envOrDefault("GITHUB_API_BASE_URL", defaultGitHubAPIBaseURL), "/")
	path := fmt.Sprintf("%s/repos/%s/%s/pulls", baseURL, url.PathEscape(owner), url.PathEscape(repo))
	q := url.Values{}
	q.Set("state", state)
	q.Set("per_page", strconv.Itoa(perPage))

	body, err := t.doJSONRequest(ctx, http.MethodGet, path+"?"+q.Encode(), nil, map[string]string{
		"Authorization": "Bearer " + token,
		"Accept":        "application/vnd.github+json",
	})
	if err != nil {
		return "", err
	}

	var prs []map[string]any
	if err := json.Unmarshal(body, &prs); err != nil {
		return "", fmt.Errorf("decode GitHub response: %w", err)
	}

	summary := make([]map[string]any, 0, len(prs))
	for _, pr := range prs {
		item := map[string]any{
			"number":     pr["number"],
			"title":      pr["title"],
			"state":      pr["state"],
			"html_url":   pr["html_url"],
			"created_at": pr["created_at"],
			"updated_at": pr["updated_at"],
			"draft":      pr["draft"],
		}
		if user, ok := pr["user"].(map[string]any); ok {
			item["author"] = user["login"]
		}
		summary = append(summary, item)
	}

	return marshalJSON(map[string]any{
		"owner":         owner,
		"repo":          repo,
		"count":         len(summary),
		"pull_requests": summary,
	})
}

func (t *localTools) githubGetIssue(ctx context.Context, args map[string]any) (string, error) {
	token := strings.TrimSpace(os.Getenv("GITHUB_TOKEN"))
	if token == "" {
		return "", fmt.Errorf("GITHUB_TOKEN is not set")
	}

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

	baseURL := strings.TrimRight(envOrDefault("GITHUB_API_BASE_URL", defaultGitHubAPIBaseURL), "/")
	endpoint := fmt.Sprintf("%s/repos/%s/%s/issues/%d", baseURL, url.PathEscape(owner), url.PathEscape(repo), number)
	body, err := t.doJSONRequest(ctx, http.MethodGet, endpoint, nil, map[string]string{
		"Authorization": "Bearer " + token,
		"Accept":        "application/vnd.github+json",
	})
	if err != nil {
		return "", err
	}

	var issue map[string]any
	if err := json.Unmarshal(body, &issue); err != nil {
		return "", fmt.Errorf("decode GitHub issue response: %w", err)
	}

	result := map[string]any{
		"number":        issue["number"],
		"title":         issue["title"],
		"state":         issue["state"],
		"html_url":      issue["html_url"],
		"created_at":    issue["created_at"],
		"updated_at":    issue["updated_at"],
		"comments":      issue["comments"],
		"body":          issue["body"],
		"pull_request":  issue["pull_request"] != nil,
		"repository":    fmt.Sprintf("%s/%s", owner, repo),
		"requested_key": number,
	}
	if user, ok := issue["user"].(map[string]any); ok {
		result["author"] = user["login"]
	}
	if assignee, ok := issue["assignee"].(map[string]any); ok {
		result["assignee"] = assignee["login"]
	}

	return marshalJSON(result)
}

func (t *localTools) jiraSearchIssues(ctx context.Context, args map[string]any) (string, error) {
	baseURL, authHeader, err := jiraConfig()
	if err != nil {
		return "", err
	}

	jql, err := getRequiredStringArg(args, "jql")
	if err != nil {
		return "", err
	}
	maxResults, err := getOptionalIntArg(args, "max_results", 20, 1, 100)
	if err != nil {
		return "", err
	}

	q := url.Values{}
	q.Set("jql", jql)
	q.Set("maxResults", strconv.Itoa(maxResults))
	q.Set("fields", "summary,status,assignee,priority,issuetype,updated,created")
	endpoint := baseURL + defaultJiraAPIPath + "/search?" + q.Encode()

	body, err := t.doJSONRequest(ctx, http.MethodGet, endpoint, nil, map[string]string{
		"Authorization": authHeader,
		"Accept":        "application/json",
	})
	if err != nil {
		return "", err
	}

	var payload struct {
		Total  int `json:"total"`
		Issues []struct {
			Key    string `json:"key"`
			Fields struct {
				Summary string `json:"summary"`
				Created string `json:"created"`
				Updated string `json:"updated"`
				Status  struct {
					Name string `json:"name"`
				} `json:"status"`
				Priority struct {
					Name string `json:"name"`
				} `json:"priority"`
				Type struct {
					Name string `json:"name"`
				} `json:"issuetype"`
				Assignee *struct {
					DisplayName string `json:"displayName"`
					Email       string `json:"emailAddress"`
				} `json:"assignee"`
			} `json:"fields"`
		} `json:"issues"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		return "", fmt.Errorf("decode Jira search response: %w", err)
	}

	issues := make([]map[string]any, 0, len(payload.Issues))
	for _, issue := range payload.Issues {
		item := map[string]any{
			"key":       issue.Key,
			"summary":   issue.Fields.Summary,
			"status":    issue.Fields.Status.Name,
			"priority":  issue.Fields.Priority.Name,
			"type":      issue.Fields.Type.Name,
			"created":   issue.Fields.Created,
			"updated":   issue.Fields.Updated,
			"issue_url": strings.TrimRight(baseURL, "/") + "/browse/" + issue.Key,
		}
		if issue.Fields.Assignee != nil {
			item["assignee"] = issue.Fields.Assignee.DisplayName
		}
		issues = append(issues, item)
	}

	return marshalJSON(map[string]any{
		"total":  payload.Total,
		"count":  len(issues),
		"issues": issues,
	})
}

func (t *localTools) jiraGetIssue(ctx context.Context, args map[string]any) (string, error) {
	baseURL, authHeader, err := jiraConfig()
	if err != nil {
		return "", err
	}

	issueKey, err := getRequiredStringArg(args, "issue_key")
	if err != nil {
		return "", err
	}

	q := url.Values{}
	q.Set("fields", "summary,status,description,assignee,priority,issuetype,updated,created,reporter")
	endpoint := fmt.Sprintf("%s%s/issue/%s?%s", baseURL, defaultJiraAPIPath, url.PathEscape(issueKey), q.Encode())

	body, err := t.doJSONRequest(ctx, http.MethodGet, endpoint, nil, map[string]string{
		"Authorization": authHeader,
		"Accept":        "application/json",
	})
	if err != nil {
		return "", err
	}

	var payload map[string]any
	if err := json.Unmarshal(body, &payload); err != nil {
		return "", fmt.Errorf("decode Jira issue response: %w", err)
	}

	return marshalJSON(payload)
}

func jiraConfig() (string, string, error) {
	baseURL := strings.TrimSpace(os.Getenv("JIRA_BASE_URL"))
	email := strings.TrimSpace(os.Getenv("JIRA_EMAIL"))
	token := strings.TrimSpace(os.Getenv("JIRA_API_TOKEN"))

	if baseURL == "" {
		return "", "", fmt.Errorf("JIRA_BASE_URL is not set")
	}
	if email == "" || token == "" {
		return "", "", fmt.Errorf("JIRA_EMAIL and JIRA_API_TOKEN are required")
	}

	baseURL = strings.TrimRight(baseURL, "/")
	auth := base64.StdEncoding.EncodeToString([]byte(email + ":" + token))
	return baseURL, "Basic " + auth, nil
}

func (t *localTools) doJSONRequest(
	ctx context.Context,
	method string,
	endpoint string,
	payload any,
	headers map[string]string,
) ([]byte, error) {
	var bodyReader io.Reader
	if payload != nil {
		raw, err := json.Marshal(payload)
		if err != nil {
			return nil, fmt.Errorf("encode request payload: %w", err)
		}
		bodyReader = bytes.NewBuffer(raw)
	}

	req, err := http.NewRequestWithContext(ctx, method, endpoint, bodyReader)
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}

	for key, value := range headers {
		req.Header.Set(key, value)
	}
	if payload != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	resp, err := t.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("request %s %s failed: %w", method, endpoint, err)
	}
	defer resp.Body.Close()

	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read response body: %w", err)
	}

	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return nil, fmt.Errorf("%s %s failed with %d: %s", method, endpoint, resp.StatusCode, truncate(raw, 500))
	}
	return raw, nil
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

func truncate(raw []byte, max int) string {
	if len(raw) <= max {
		return string(raw)
	}
	return string(raw[:max]) + "...(truncated)"
}
