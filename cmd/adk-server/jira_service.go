package main

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
)

const defaultJiraAPIPath = "/rest/api/3"

type JiraService struct {
	httpClient *http.Client
	baseURL    string
	authHeader string
	logger     *slog.Logger
}

func NewJiraServiceFromEnv(httpClient *http.Client, logger *slog.Logger) *JiraService {
	if logger == nil {
		logger = slog.Default()
	}
	baseURL := strings.TrimRight(strings.TrimSpace(os.Getenv("JIRA_BASE_URL")), "/")
	email := strings.TrimSpace(os.Getenv("JIRA_EMAIL"))
	token := strings.TrimSpace(os.Getenv("JIRA_API_TOKEN"))
	auth := ""
	if email != "" && token != "" {
		auth = "Basic " + base64.StdEncoding.EncodeToString([]byte(email+":"+token))
	}

	s := &JiraService{
		httpClient: httpClient,
		baseURL:    baseURL,
		authHeader: auth,
		logger:     logger,
	}
	s.logger.Info("Jira service configured", "base_url", s.baseURL, "auth_configured", s.authHeader != "")
	return s
}

func (s *JiraService) ensureConfigured() error {
	if strings.TrimSpace(s.baseURL) == "" {
		return fmt.Errorf("JIRA_BASE_URL is not set")
	}
	if strings.TrimSpace(s.authHeader) == "" {
		return fmt.Errorf("JIRA_EMAIL and JIRA_API_TOKEN are required")
	}
	return nil
}

func (s *JiraService) GetAssignedIssues(ctx context.Context, assignee string, maxResults, updatedWithinDays int) (jiraAssignedIssuesResult, error) {
	if err := s.ensureConfigured(); err != nil {
		s.logger.Error("Jira service not configured", "method", "GetAssignedIssues", "error", err)
		return jiraAssignedIssuesResult{}, err
	}
	s.logger.Info("Jira API call", "method", "GetAssignedIssues", "assignee", assignee, "max_results", maxResults, "updated_within_days", updatedWithinDays)

	safeAssignee := strings.ReplaceAll(assignee, `"`, `\"`)
	jql := fmt.Sprintf(`assignee = "%s"`, safeAssignee)
	if updatedWithinDays > 0 {
		jql += fmt.Sprintf(" AND updated >= -%dd", updatedWithinDays)
	}
	jql += " ORDER BY updated DESC"

	payload, err := s.search(ctx, jql, maxResults)
	if err != nil {
		return jiraAssignedIssuesResult{}, err
	}

	issues := buildJiraIssueSummaries(payload.Issues, s.baseURL)
	result := jiraAssignedIssuesResult{
		Assignee: assignee,
		Total:    payload.Total,
		Count:    len(issues),
		Issues:   issues,
	}
	s.logger.Info("Jira API result", "method", "GetAssignedIssues", "assignee", assignee, "count", result.Count, "total", result.Total)
	return result, nil
}

func (s *JiraService) SearchIssues(ctx context.Context, jql string, maxResults int) (jiraSearchIssuesResult, error) {
	if err := s.ensureConfigured(); err != nil {
		s.logger.Error("Jira service not configured", "method", "SearchIssues", "error", err)
		return jiraSearchIssuesResult{}, err
	}
	s.logger.Info("Jira API call", "method", "SearchIssues", "max_results", maxResults)

	payload, err := s.search(ctx, jql, maxResults)
	if err != nil {
		return jiraSearchIssuesResult{}, err
	}

	issues := buildJiraIssueSummaries(payload.Issues, s.baseURL)
	result := jiraSearchIssuesResult{
		Total:  payload.Total,
		Count:  len(issues),
		Issues: issues,
	}
	s.logger.Info("Jira API result", "method", "SearchIssues", "count", result.Count, "total", result.Total)
	return result, nil
}

func (s *JiraService) GetIssue(ctx context.Context, issueKey string) (map[string]any, error) {
	if err := s.ensureConfigured(); err != nil {
		s.logger.Error("Jira service not configured", "method", "GetIssue", "error", err)
		return nil, err
	}
	s.logger.Info("Jira API call", "method", "GetIssue", "issue_key", issueKey)

	q := url.Values{}
	q.Set("fields", "summary,status,description,assignee,priority,issuetype,updated,created,reporter")
	endpoint := fmt.Sprintf("%s%s/issue/%s?%s", s.baseURL, defaultJiraAPIPath, url.PathEscape(issueKey), q.Encode())

	body, err := doJSONRequest(ctx, s.httpClient, http.MethodGet, endpoint, nil, map[string]string{
		"Authorization": s.authHeader,
		"Accept":        "application/json",
	})
	if err != nil {
		return nil, err
	}

	var payload map[string]any
	if err := json.Unmarshal(body, &payload); err != nil {
		return nil, fmt.Errorf("decode Jira issue response: %w", err)
	}
	s.logger.Info("Jira API result", "method", "GetIssue", "issue_key", issueKey)
	return payload, nil
}

func (s *JiraService) search(ctx context.Context, jql string, maxResults int) (jiraSearchResponse, error) {
	q := url.Values{}
	q.Set("jql", jql)
	q.Set("maxResults", strconv.Itoa(maxResults))
	q.Set("fields", "summary,status,assignee,priority,issuetype,updated,created")
	endpoint := s.baseURL + defaultJiraAPIPath + "/search/jql?" + q.Encode()

	body, err := doJSONRequest(ctx, s.httpClient, http.MethodGet, endpoint, nil, map[string]string{
		"Authorization": s.authHeader,
		"Accept":        "application/json",
	})
	if err != nil {
		return jiraSearchResponse{}, err
	}

	var payload jiraSearchResponse
	if err := json.Unmarshal(body, &payload); err != nil {
		return jiraSearchResponse{}, fmt.Errorf("decode Jira search response: %w", err)
	}
	return payload, nil
}

func buildJiraIssueSummaries(issues []jiraIssue, baseURL string) []jiraIssueSummary {
	summaries := make([]jiraIssueSummary, 0, len(issues))
	for _, issue := range issues {
		item := jiraIssueSummary{
			Key:      issue.Key,
			Summary:  issue.Fields.Summary,
			Status:   issue.Fields.Status.Name,
			Priority: issue.Fields.Priority.Name,
			Type:     issue.Fields.Type.Name,
			Created:  issue.Fields.Created,
			Updated:  issue.Fields.Updated,
			IssueURL: strings.TrimRight(baseURL, "/") + "/browse/" + issue.Key,
		}
		if issue.Fields.Assignee != nil {
			item.Assignee = issue.Fields.Assignee.DisplayName
		}
		summaries = append(summaries, item)
	}
	return summaries
}
