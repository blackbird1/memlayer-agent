package main

import "time"

type jiraNamedValue struct {
	Name string `json:"name"`
}

type jiraAssignee struct {
	DisplayName string `json:"displayName"`
	Email       string `json:"emailAddress"`
}

type jiraIssueFields struct {
	Summary  string         `json:"summary"`
	Created  string         `json:"created"`
	Updated  string         `json:"updated"`
	Status   jiraNamedValue `json:"status"`
	Priority jiraNamedValue `json:"priority"`
	Type     jiraNamedValue `json:"issuetype"`
	Assignee *jiraAssignee  `json:"assignee"`
}

type jiraIssue struct {
	Key    string          `json:"key"`
	Fields jiraIssueFields `json:"fields"`
}

type jiraSearchResponse struct {
	Total  int         `json:"total"`
	Issues []jiraIssue `json:"issues"`
}

type jiraIssueSummary struct {
	Key      string `json:"key"`
	Summary  string `json:"summary"`
	Status   string `json:"status"`
	Priority string `json:"priority"`
	Type     string `json:"type"`
	Created  string `json:"created"`
	Updated  string `json:"updated"`
	IssueURL string `json:"issue_url"`
	Assignee string `json:"assignee,omitempty"`
}

type jiraAssignedIssuesResult struct {
	Assignee string             `json:"assignee"`
	Total    int                `json:"total"`
	Count    int                `json:"count"`
	Issues   []jiraIssueSummary `json:"issues"`
}

type jiraSearchIssuesResult struct {
	Total  int                `json:"total"`
	Count  int                `json:"count"`
	Issues []jiraIssueSummary `json:"issues"`
}

type githubRepoRef struct {
	Name string `json:"name"`
}

type githubCommitRef struct {
	SHA     string `json:"sha"`
	Message string `json:"message"`
}

type githubEventPayload struct {
	Commits []githubCommitRef `json:"commits"`
}

type githubUserEvent struct {
	Type      string             `json:"type"`
	CreatedAt time.Time          `json:"created_at"`
	Repo      githubRepoRef      `json:"repo"`
	Payload   githubEventPayload `json:"payload"`
}

type githubCommitActivity struct {
	Repo     string `json:"repo"`
	SHA      string `json:"sha"`
	Message  string `json:"message"`
	PushedAt string `json:"pushed_at"`
}

type githubRecentCommitsResult struct {
	Username  string                 `json:"username"`
	SinceDays int                    `json:"since_days"`
	Count     int                    `json:"count"`
	Commits   []githubCommitActivity `json:"commits"`
}

type githubSearchIssueItem struct {
	Number        int    `json:"number"`
	Title         string `json:"title"`
	State         string `json:"state"`
	HTMLURL       string `json:"html_url"`
	CreatedAt     string `json:"created_at"`
	UpdatedAt     string `json:"updated_at"`
	RepositoryURL string `json:"repository_url"`
}

type githubSearchIssuesResponse struct {
	TotalCount int                     `json:"total_count"`
	Items      []githubSearchIssueItem `json:"items"`
}

type githubSearchCommitsResponse struct {
	TotalCount int                      `json:"total_count"`
	Items      []githubSearchCommitItem `json:"items"`
}

type githubSearchCommitItem struct {
	SHA        string `json:"sha"`
	HTMLURL    string `json:"html_url"`
	Repository struct {
		FullName string `json:"full_name"`
	} `json:"repository"`
	Commit struct {
		Message   string `json:"message"`
		Committer struct {
			Date string `json:"date"`
		} `json:"committer"`
	} `json:"commit"`
}

type githubPullRequestSummary struct {
	Number           int    `json:"number"`
	Title            string `json:"title"`
	State            string `json:"state"`
	HTMLURL          string `json:"html_url"`
	CreatedAt        string `json:"created_at"`
	UpdatedAt        string `json:"updated_at"`
	RepositoryAPIURL string `json:"repository_api_url,omitempty"`
}

type githubActivePullRequestsResult struct {
	Username     string                     `json:"username"`
	Total        int                        `json:"total"`
	Count        int                        `json:"count"`
	PullRequests []githubPullRequestSummary `json:"pull_requests"`
}

type githubRepositoryActivity struct {
	Repository       string   `json:"repository"`
	LastActivityAt   string   `json:"last_activity_at"`
	RecentEventTypes []string `json:"recent_event_types"`
}

type githubRecentRepositoriesResult struct {
	Username     string                     `json:"username"`
	SinceDays    int                        `json:"since_days"`
	Count        int                        `json:"count"`
	Repositories []githubRepositoryActivity `json:"repositories"`
}
