package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"
)

const defaultGitHubAPIBaseURL = "https://api.github.com"

type GitHubService struct {
	httpClient *http.Client
	token      string
	baseURL    string
	logger     *slog.Logger
}

func NewGitHubServiceFromEnv(httpClient *http.Client, logger *slog.Logger) *GitHubService {
	if logger == nil {
		logger = slog.Default()
	}
	s := &GitHubService{
		httpClient: httpClient,
		token:      strings.TrimSpace(os.Getenv("GITHUB_TOKEN")),
		baseURL:    strings.TrimRight(envOrDefault("GITHUB_API_BASE_URL", defaultGitHubAPIBaseURL), "/"),
		logger:     logger,
	}
	s.logger.Info("GitHub service configured", "base_url", s.baseURL, "token_configured", s.token != "")
	return s
}

func (s *GitHubService) ensureConfigured() error {
	if strings.TrimSpace(s.token) == "" {
		return fmt.Errorf("GITHUB_TOKEN is not set")
	}
	return nil
}

func (s *GitHubService) GetRecentCommits(ctx context.Context, username string, sinceDays, maxResults int) (githubRecentCommitsResult, error) {
	if err := s.ensureConfigured(); err != nil {
		s.logger.Error("GitHub service not configured", "method", "GetRecentCommits", "error", err)
		return githubRecentCommitsResult{}, err
	}
	s.logger.Info("GitHub API call", "method", "GetRecentCommits", "username", username, "since_days", sinceDays, "max_results", maxResults)

	sinceDate := time.Now().AddDate(0, 0, -sinceDays).Format("2006-01-02")
	commitQuery := url.QueryEscape(fmt.Sprintf("author:%s committer-date:>=%s", username, sinceDate))
	perPage := min(maxResults, 100)
	endpoint := fmt.Sprintf("%s/search/commits?q=%s&sort=committer-date&order=desc&per_page=%d", s.baseURL, commitQuery, perPage)
	body, err := doJSONRequest(ctx, s.httpClient, http.MethodGet, endpoint, nil, map[string]string{
		"Authorization": "Bearer " + s.token,
		"Accept":        "application/vnd.github+json",
	})
	if err != nil {
		return githubRecentCommitsResult{}, err
	}

	var payload githubSearchCommitsResponse
	if err := json.Unmarshal(body, &payload); err != nil {
		return githubRecentCommitsResult{}, fmt.Errorf("decode GitHub commit search response: %w", err)
	}

	commits := make([]githubCommitActivity, 0)
	for _, item := range payload.Items {
		commits = append(commits, githubCommitActivity{
			Repo:     item.Repository.FullName,
			SHA:      item.SHA,
			Message:  item.Commit.Message,
			PushedAt: item.Commit.Committer.Date,
		})
		if len(commits) >= maxResults {
			break
		}
	}

	result := githubRecentCommitsResult{
		Username:  username,
		SinceDays: sinceDays,
		Count:     len(commits),
		Commits:   commits,
	}
	s.logger.Info("GitHub API result", "method", "GetRecentCommits", "username", username, "count", result.Count)
	return result, nil
}

func (s *GitHubService) GetActivePullRequests(ctx context.Context, username string, maxResults int) (githubActivePullRequestsResult, error) {
	if err := s.ensureConfigured(); err != nil {
		s.logger.Error("GitHub service not configured", "method", "GetActivePullRequests", "error", err)
		return githubActivePullRequestsResult{}, err
	}
	s.logger.Info("GitHub API call", "method", "GetActivePullRequests", "username", username, "max_results", maxResults)

	q := url.Values{}
	q.Set("q", fmt.Sprintf("is:pr+author:%s+is:open", username))
	q.Set("sort", "updated")
	q.Set("order", "desc")
	q.Set("per_page", strconv.Itoa(maxResults))
	endpoint := fmt.Sprintf("%s/search/issues?%s", s.baseURL, q.Encode())

	body, err := doJSONRequest(ctx, s.httpClient, http.MethodGet, endpoint, nil, map[string]string{
		"Authorization": "Bearer " + s.token,
		"Accept":        "application/vnd.github+json",
	})
	if err != nil {
		return githubActivePullRequestsResult{}, err
	}

	var payload githubSearchIssuesResponse
	if err := json.Unmarshal(body, &payload); err != nil {
		return githubActivePullRequestsResult{}, fmt.Errorf("decode GitHub PR search response: %w", err)
	}

	prs := make([]githubPullRequestSummary, 0, len(payload.Items))
	for _, item := range payload.Items {
		pr := githubPullRequestSummary{
			Number:    item.Number,
			Title:     item.Title,
			State:     item.State,
			HTMLURL:   item.HTMLURL,
			CreatedAt: item.CreatedAt,
			UpdatedAt: item.UpdatedAt,
		}
		if item.RepositoryURL != "" {
			pr.RepositoryAPIURL = item.RepositoryURL
		}
		prs = append(prs, pr)
	}

	result := githubActivePullRequestsResult{
		Username:     username,
		Total:        payload.TotalCount,
		Count:        len(prs),
		PullRequests: prs,
	}
	s.logger.Info("GitHub API result", "method", "GetActivePullRequests", "username", username, "count", result.Count, "total", result.Total)
	return result, nil
}

func (s *GitHubService) ListRecentContributedRepositories(ctx context.Context, username string, sinceDays, maxResults int) (githubRecentRepositoriesResult, error) {
	if err := s.ensureConfigured(); err != nil {
		s.logger.Error("GitHub service not configured", "method", "ListRecentContributedRepositories", "error", err)
		return githubRecentRepositoriesResult{}, err
	}
	s.logger.Info("GitHub API call", "method", "ListRecentContributedRepositories", "username", username, "since_days", sinceDays, "max_results", maxResults)

	sinceDate := time.Now().AddDate(0, 0, -sinceDays).Format("2006-01-02")
	type repoStats struct {
		LastActivity time.Time
		EventTypes   map[string]bool
	}
	repos := make(map[string]*repoStats)

	commitQuery := url.QueryEscape(fmt.Sprintf("author:%s committer-date:>=%s", username, sinceDate))
	commitEndpoint := fmt.Sprintf("%s/search/commits?q=%s&sort=committer-date&order=desc&per_page=100", s.baseURL, commitQuery)
	commitBody, err := doJSONRequest(ctx, s.httpClient, http.MethodGet, commitEndpoint, nil, map[string]string{
		"Authorization": "Bearer " + s.token,
		"Accept":        "application/vnd.github+json",
	})
	if err != nil {
		return githubRecentRepositoriesResult{}, err
	}
	var commitPayload githubSearchCommitsResponse
	if err := json.Unmarshal(commitBody, &commitPayload); err != nil {
		return githubRecentRepositoriesResult{}, fmt.Errorf("decode GitHub commit search response: %w", err)
	}
	for _, item := range commitPayload.Items {
		repoName := strings.TrimSpace(item.Repository.FullName)
		if repoName == "" {
			continue
		}
		commitTime, err := parseGitHubTimestamp(item.Commit.Committer.Date)
		if err != nil {
			continue
		}
		stats, exists := repos[repoName]
		if !exists {
			stats = &repoStats{LastActivity: commitTime, EventTypes: map[string]bool{}}
			repos[repoName] = stats
		}
		if commitTime.After(stats.LastActivity) {
			stats.LastActivity = commitTime
		}
		stats.EventTypes["Commit"] = true
	}

	prQuery := url.QueryEscape(fmt.Sprintf("is:pr author:%s updated:>=%s", username, sinceDate))
	prEndpoint := fmt.Sprintf("%s/search/issues?q=%s&sort=updated&order=desc&per_page=100", s.baseURL, prQuery)
	prBody, err := doJSONRequest(ctx, s.httpClient, http.MethodGet, prEndpoint, nil, map[string]string{
		"Authorization": "Bearer " + s.token,
		"Accept":        "application/vnd.github+json",
	})
	if err != nil {
		return githubRecentRepositoriesResult{}, err
	}
	var prPayload githubSearchIssuesResponse
	if err := json.Unmarshal(prBody, &prPayload); err != nil {
		return githubRecentRepositoriesResult{}, fmt.Errorf("decode GitHub PR search response: %w", err)
	}
	for _, item := range prPayload.Items {
		repoName := repositoryNameFromAPIURL(item.RepositoryURL)
		if repoName == "" {
			continue
		}
		updatedAt, err := parseGitHubTimestamp(item.UpdatedAt)
		if err != nil {
			continue
		}
		stats, exists := repos[repoName]
		if !exists {
			stats = &repoStats{LastActivity: updatedAt, EventTypes: map[string]bool{}}
			repos[repoName] = stats
		}
		if updatedAt.After(stats.LastActivity) {
			stats.LastActivity = updatedAt
		}
		stats.EventTypes["PullRequest"] = true
	}

	type repoEntry struct {
		Name         string
		LastActivity time.Time
		EventTypes   []string
	}
	entries := make([]repoEntry, 0, len(repos))
	for name, stats := range repos {
		types := make([]string, 0, len(stats.EventTypes))
		for eventType := range stats.EventTypes {
			types = append(types, eventType)
		}
		sort.Strings(types)
		entries = append(entries, repoEntry{Name: name, LastActivity: stats.LastActivity, EventTypes: types})
	}

	sort.Slice(entries, func(i, j int) bool {
		return entries[i].LastActivity.After(entries[j].LastActivity)
	})
	if len(entries) > maxResults {
		entries = entries[:maxResults]
	}

	result := make([]githubRepositoryActivity, 0, len(entries))
	for _, entry := range entries {
		result = append(result, githubRepositoryActivity{
			Repository:       entry.Name,
			LastActivityAt:   entry.LastActivity.Format(time.RFC3339),
			RecentEventTypes: entry.EventTypes,
		})
	}

	resultPayload := githubRecentRepositoriesResult{
		Username:     username,
		SinceDays:    sinceDays,
		Count:        len(result),
		Repositories: result,
	}
	s.logger.Info("GitHub API result", "method", "ListRecentContributedRepositories", "username", username, "count", resultPayload.Count)
	return resultPayload, nil
}

func (s *GitHubService) ListPullRequests(ctx context.Context, owner, repo, state string, perPage int) (map[string]any, error) {
	if err := s.ensureConfigured(); err != nil {
		s.logger.Error("GitHub service not configured", "method", "ListPullRequests", "error", err)
		return nil, err
	}
	s.logger.Info("GitHub API call", "method", "ListPullRequests", "owner", owner, "repo", repo, "state", state, "per_page", perPage)

	path := fmt.Sprintf("%s/repos/%s/%s/pulls", s.baseURL, url.PathEscape(owner), url.PathEscape(repo))
	q := url.Values{}
	q.Set("state", state)
	q.Set("per_page", strconv.Itoa(perPage))

	body, err := doJSONRequest(ctx, s.httpClient, http.MethodGet, path+"?"+q.Encode(), nil, map[string]string{
		"Authorization": "Bearer " + s.token,
		"Accept":        "application/vnd.github+json",
	})
	if err != nil {
		return nil, err
	}

	var prs []map[string]any
	if err := json.Unmarshal(body, &prs); err != nil {
		return nil, fmt.Errorf("decode GitHub response: %w", err)
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

	result := map[string]any{
		"owner":         owner,
		"repo":          repo,
		"count":         len(summary),
		"pull_requests": summary,
	}
	s.logger.Info("GitHub API result", "method", "ListPullRequests", "owner", owner, "repo", repo, "count", len(summary))
	return result, nil
}

func (s *GitHubService) GetIssue(ctx context.Context, owner, repo string, number int) (map[string]any, error) {
	if err := s.ensureConfigured(); err != nil {
		s.logger.Error("GitHub service not configured", "method", "GetIssue", "error", err)
		return nil, err
	}
	s.logger.Info("GitHub API call", "method", "GetIssue", "owner", owner, "repo", repo, "number", number)

	endpoint := fmt.Sprintf("%s/repos/%s/%s/issues/%d", s.baseURL, url.PathEscape(owner), url.PathEscape(repo), number)
	body, err := doJSONRequest(ctx, s.httpClient, http.MethodGet, endpoint, nil, map[string]string{
		"Authorization": "Bearer " + s.token,
		"Accept":        "application/vnd.github+json",
	})
	if err != nil {
		return nil, err
	}

	var issue map[string]any
	if err := json.Unmarshal(body, &issue); err != nil {
		return nil, fmt.Errorf("decode GitHub issue response: %w", err)
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
	s.logger.Info("GitHub API result", "method", "GetIssue", "owner", owner, "repo", repo, "number", number)
	return result, nil
}

func parseGitHubTimestamp(value string) (time.Time, error) {
	return time.Parse(time.RFC3339, strings.TrimSpace(value))
}

func repositoryNameFromAPIURL(raw string) string {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return ""
	}
	u, err := url.Parse(trimmed)
	if err != nil {
		return ""
	}
	parts := strings.Split(strings.Trim(u.Path, "/"), "/")
	if len(parts) < 3 {
		return ""
	}
	if parts[0] != "repos" {
		return ""
	}
	return parts[1] + "/" + parts[2]
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
