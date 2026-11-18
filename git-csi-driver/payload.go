package main

// payload.go parses the JSON body of a push webhook.

import "encoding/json"

// push is a parsed webhook body: the ref that was pushed, and every URL
// the payload gives for the repository.
type push struct {
	ref  string
	urls []string
}

// pushPayload has the URL fields of a push body from GitHub, GitLab,
// Gitea, and Forgejo, because each forge puts the repository URL in
// different fields. GitHub, Gitea, and Forgejo set repository. GitLab
// sets project and repository.url.
type pushPayload struct {
	Ref        string `json:"ref"`
	Repository struct {
		CloneURL string `json:"clone_url"`
		SSHURL   string `json:"ssh_url"`
		GitURL   string `json:"git_url"`
		HTMLURL  string `json:"html_url"`
		URL      string `json:"url"`
	} `json:"repository"`
	Project struct {
		GitHTTPURL string `json:"git_http_url"`
		GitSSHURL  string `json:"git_ssh_url"`
		WebURL     string `json:"web_url"`
	} `json:"project"`
}

// parsePush returns the push in a webhook body. The second result is
// false when the body is not JSON, has no ref, or has no repository
// URL.
func parsePush(body []byte) (push, bool) {
	var payload pushPayload
	if err := json.Unmarshal(body, &payload); err != nil {
		return push{}, false
	}
	if payload.Ref == "" {
		return push{}, false
	}
	pushed := push{ref: payload.Ref}
	for _, url := range []string{
		payload.Repository.CloneURL,
		payload.Repository.SSHURL,
		payload.Repository.GitURL,
		payload.Repository.HTMLURL,
		payload.Repository.URL,
		payload.Project.GitHTTPURL,
		payload.Project.GitSSHURL,
		payload.Project.WebURL,
	} {
		if url != "" {
			pushed.urls = append(pushed.urls, url)
		}
	}
	if len(pushed.urls) == 0 {
		return push{}, false
	}
	return pushed, true
}
