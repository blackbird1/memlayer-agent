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

func (s *JiraService) CreateIssue(
	ctx context.Context,
	projectKey string,
	summary string,
	description string,
	issueType string,
	assigneeID string,
	priorityName string,
) (jiraCreateIssueResult, error) {
	if err := s.ensureConfigured(); err != nil {
		s.logger.Error("Jira service not configured", "method", "CreateIssue", "error", err)
		return jiraCreateIssueResult{}, err
	}
	s.logger.Info("Jira API call", "method", "CreateIssue", "project_key", projectKey, "issue_type", issueType)

	fields := map[string]any{
		"project": map[string]any{
			"key": projectKey,
		},
		"summary": summary,
		"issuetype": map[string]any{
			"name": issueType,
		},
	}

	if strings.TrimSpace(description) != "" {
		fields["description"] = map[string]any{
			"type":    "doc",
			"version": 1,
			"content": []map[string]any{
				{
					"type": "paragraph",
					"content": []map[string]any{
						{
							"type": "text",
							"text": description,
						},
					},
				},
			},
		}
	}
	if strings.TrimSpace(assigneeID) != "" {
		fields["assignee"] = map[string]any{
			"accountId": assigneeID,
		}
	}
	if strings.TrimSpace(priorityName) != "" {
		fields["priority"] = map[string]any{
			"name": priorityName,
		}
	}

	endpoint := s.baseURL + defaultJiraAPIPath + "/issue"
	body, err := doJSONRequest(ctx, s.httpClient, http.MethodPost, endpoint, map[string]any{
		"fields": fields,
	}, map[string]string{
		"Authorization": s.authHeader,
		"Accept":        "application/json",
	})
	if err != nil {
		return jiraCreateIssueResult{}, err
	}

	var created jiraCreateIssueResult
	if err := json.Unmarshal(body, &created); err != nil {
		return jiraCreateIssueResult{}, fmt.Errorf("decode Jira create issue response: %w", err)
	}
	if strings.TrimSpace(created.Key) != "" {
		created.IssueURL = strings.TrimRight(s.baseURL, "/") + "/browse/" + created.Key
	}
	s.logger.Info("Jira API result", "method", "CreateIssue", "key", created.Key, "id", created.ID)
	return created, nil
}

func (s *JiraService) ListProjects(ctx context.Context, maxResults int) (jiraListProjectsResult, error) {
	if err := s.ensureConfigured(); err != nil {
		s.logger.Error("Jira service not configured", "method", "ListProjects", "error", err)
		return jiraListProjectsResult{}, err
	}
	s.logger.Info("Jira API call", "method", "ListProjects", "max_results", maxResults)

	q := url.Values{}
	q.Set("maxResults", strconv.Itoa(maxResults))
	endpoint := s.baseURL + defaultJiraAPIPath + "/project/search?" + q.Encode()

	body, err := doJSONRequest(ctx, s.httpClient, http.MethodGet, endpoint, nil, map[string]string{
		"Authorization": s.authHeader,
		"Accept":        "application/json",
	})
	if err != nil {
		return jiraListProjectsResult{}, err
	}

	var payload jiraProjectSearchResponse
	if err := json.Unmarshal(body, &payload); err != nil {
		return jiraListProjectsResult{}, fmt.Errorf("decode Jira project search response: %w", err)
	}

	projects := make([]jiraProject, 0, len(payload.Values))
	for _, item := range payload.Values {
		if strings.TrimSpace(item.Key) != "" {
			item.ProjectURL = strings.TrimRight(s.baseURL, "/") + "/browse/" + item.Key
		}
		projects = append(projects, item)
	}

	result := jiraListProjectsResult{
		Total:    payload.Total,
		Count:    len(projects),
		Projects: projects,
	}
	s.logger.Info("Jira API result", "method", "ListProjects", "count", result.Count, "total", result.Total)
	return result, nil
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
