package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
)

// Runs reads the workflow runs of one repository from the GitHub API.
type Runs struct {
	// API is the API's address, https://api.github.com.
	API string
	// Repository is owner/name, such as liken-sh/liken.
	Repository string
	Token      string
	Client     *http.Client
}

// NewestGreen is the head commit of the newest push to main whose run
// of the workflow file passed, or "" when no run passed.
func (r Runs) NewestGreen(workflow string) (string, error) {
	u := fmt.Sprintf("%s/repos/%s/actions/workflows/%s/runs?%s", r.API, r.Repository, url.PathEscape(workflow),
		url.Values{"branch": {"main"}, "event": {"push"}, "status": {"success"}, "per_page": {"1"}, "exclude_pull_requests": {"true"}}.Encode())
	req, err := http.NewRequest(http.MethodGet, u, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	if r.Token != "" {
		req.Header.Set("Authorization", "Bearer "+r.Token)
	}
	client := r.Client
	if client == nil {
		client = http.DefaultClient
	}
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("the GitHub API answered %s for the runs of %s", resp.Status, workflow)
	}
	var body struct {
		Runs []struct {
			HeadSHA string `json:"head_sha"`
		} `json:"workflow_runs"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return "", err
	}
	if len(body.Runs) == 0 {
		return "", nil
	}
	return body.Runs[0].HeadSHA, nil
}
