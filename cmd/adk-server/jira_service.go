package main

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
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

func (s *JiraService) AssignIssue(ctx context.Context, issueKey, assigneeID string) (jiraAssignIssueResult, error) {
	if err := s.ensureConfigured(); err != nil {
		s.logger.Error("Jira service not configured", "method", "AssignIssue", "error", err)
		return jiraAssignIssueResult{}, err
	}
	s.logger.Info("Jira API call", "method", "AssignIssue", "issue_key", issueKey)

	endpoint := fmt.Sprintf("%s%s/issue/%s/assignee", s.baseURL, defaultJiraAPIPath, url.PathEscape(issueKey))
	_, err := doJSONRequest(ctx, s.httpClient, http.MethodPut, endpoint, map[string]any{
		"accountId": assigneeID,
	}, map[string]string{
		"Authorization": s.authHeader,
		"Accept":        "application/json",
	})
	if err != nil {
		return jiraAssignIssueResult{}, err
	}

	result := jiraAssignIssueResult{
		IssueKey:   issueKey,
		AssigneeID: assigneeID,
		IssueURL:   strings.TrimRight(s.baseURL, "/") + "/browse/" + issueKey,
		Assigned:   true,
	}
	s.logger.Info("Jira API result", "method", "AssignIssue", "issue_key", issueKey, "assignee_id", assigneeID)
	return result, nil
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

func (s *JiraService) ValidateConnection(ctx context.Context, projectKey string, maxResults int) (jiraValidateConnectionResult, error) {
	if err := s.ensureConfigured(); err != nil {
		s.logger.Error("Jira service not configured", "method", "ValidateConnection", "error", err)
		return jiraValidateConnectionResult{}, err
	}
	s.logger.Info("Jira API call", "method", "ValidateConnection", "project_key", projectKey, "max_results", maxResults)

	result := jiraValidateConnectionResult{
		BaseURL:    s.baseURL,
		ProjectKey: strings.TrimSpace(projectKey),
	}

	myselfEndpoint := s.baseURL + defaultJiraAPIPath + "/myself"
	authStatus, authBody, err := s.doRawJSONRequest(ctx, http.MethodGet, myselfEndpoint, nil)
	if err != nil {
		return jiraValidateConnectionResult{}, err
	}
	result.AuthStatusCode = authStatus
	result.Authenticated = authStatus >= 200 && authStatus <= 299
	if result.Authenticated {
		var me jiraMyselfResponse
		if err := json.Unmarshal(authBody, &me); err == nil {
			result.Account = jiraValidationAccount{
				AccountID:   me.AccountID,
				DisplayName: me.DisplayName,
				Email:       me.Email,
			}
		}
	} else {
		result.AuthError = truncate(authBody, 300)
	}

	projectSearchURL := s.baseURL + defaultJiraAPIPath + "/project/search?" + url.Values{
		"maxResults": []string{strconv.Itoa(maxResults)},
	}.Encode()
	projectStatus, projectsBody, err := s.doRawJSONRequest(ctx, http.MethodGet, projectSearchURL, nil)
	if err != nil {
		return jiraValidateConnectionResult{}, err
	}
	if projectStatus >= 200 && projectStatus <= 299 {
		var payload jiraProjectSearchResponse
		if err := json.Unmarshal(projectsBody, &payload); err == nil {
			projects := make([]jiraProject, 0, len(payload.Values))
			for _, item := range payload.Values {
				if strings.TrimSpace(item.Key) != "" {
					item.ProjectURL = strings.TrimRight(s.baseURL, "/") + "/browse/" + item.Key
				}
				projects = append(projects, item)
			}
			result.VisibleProjectCount = payload.Total
			result.VisibleProjects = projects
		}
	}

	if result.ProjectKey != "" {
		projectEndpoint := fmt.Sprintf("%s%s/project/%s", s.baseURL, defaultJiraAPIPath, url.PathEscape(result.ProjectKey))
		projectStatusCode, _, err := s.doRawJSONRequest(ctx, http.MethodGet, projectEndpoint, nil)
		if err != nil {
			return jiraValidateConnectionResult{}, err
		}
		result.ProjectFound = projectStatusCode >= 200 && projectStatusCode <= 299

		permissionsURL := s.baseURL + defaultJiraAPIPath + "/mypermissions?" + url.Values{
			"projectKey":  []string{result.ProjectKey},
			"permissions": []string{"BROWSE_PROJECTS,CREATE_ISSUES,EDIT_ISSUES"},
		}.Encode()
		permStatus, permBody, err := s.doRawJSONRequest(ctx, http.MethodGet, permissionsURL, nil)
		if err != nil {
			return jiraValidateConnectionResult{}, err
		}
		result.PermissionStatus = permStatus
		if permStatus >= 200 && permStatus <= 299 {
			var permissions jiraMyPermissionsResponse
			if err := json.Unmarshal(permBody, &permissions); err == nil {
				result.Permissions = jiraValidationPermissions{
					BrowseProjects: permissions.Permissions["BROWSE_PROJECTS"].HavePermission,
					CreateIssues:   permissions.Permissions["CREATE_ISSUES"].HavePermission,
					EditIssues:     permissions.Permissions["EDIT_ISSUES"].HavePermission,
				}
			}
		}
	}

	result.Recommendations = buildJiraValidationRecommendations(result)
	s.logger.Info(
		"Jira API result",
		"method", "ValidateConnection",
		"authenticated", result.Authenticated,
		"visible_project_count", result.VisibleProjectCount,
		"project_key", result.ProjectKey,
		"project_found", result.ProjectFound,
		"create_issues", result.Permissions.CreateIssues,
	)
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

func buildJiraValidationRecommendations(result jiraValidateConnectionResult) []string {
	recommendations := make([]string, 0, 4)
	if !result.Authenticated {
		recommendations = append(recommendations, "Authentication failed. Verify JIRA_EMAIL and JIRA_API_TOKEN, then restart the server.")
	}
	if result.VisibleProjectCount == 0 {
		recommendations = append(recommendations, "No visible projects were returned. Confirm this Jira user has access to at least one project.")
	}
	if result.ProjectKey != "" && !result.ProjectFound {
		recommendations = append(recommendations, "The provided project key was not found. Confirm the exact project key in Jira project settings.")
	}
	if result.ProjectKey != "" && result.ProjectFound && !result.Permissions.CreateIssues {
		recommendations = append(recommendations, "User lacks CREATE_ISSUES permission in the project. Update the project permission scheme.")
	}
	if len(recommendations) == 0 {
		recommendations = append(recommendations, "Connection and project permissions look valid.")
	}
	return recommendations
}

func (s *JiraService) doRawJSONRequest(ctx context.Context, method, endpoint string, payload any) (int, []byte, error) {
	var bodyReader io.Reader
	if payload != nil {
		raw, err := json.Marshal(payload)
		if err != nil {
			return 0, nil, fmt.Errorf("encode request payload: %w", err)
		}
		bodyReader = bytes.NewBuffer(raw)
	}
	req, err := http.NewRequestWithContext(ctx, method, endpoint, bodyReader)
	if err != nil {
		return 0, nil, fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Authorization", s.authHeader)
	req.Header.Set("Accept", "application/json")
	if payload != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := s.httpClient.Do(req)
	if err != nil {
		return 0, nil, fmt.Errorf("request %s %s failed: %w", method, endpoint, err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return resp.StatusCode, nil, fmt.Errorf("read response body: %w", err)
	}
	return resp.StatusCode, body, nil
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
