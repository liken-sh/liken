package main

// payload.go reads a push out of a forge's JSON. The four forges name
// the repository in different fields, and the reader takes every one of
// them it finds.

import "encoding/json"

// push is what the listener read: the ref the forge pushed, and every
// URL the payload names for the repository.
type push struct {
	ref  string
	urls []string
}

// pushPayload is the union of the four forges' push bodies. GitHub,
// Gitea, and Forgejo fill repository. GitLab fills project and
// repository.url. A field a forge does not send stays empty.
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

// parsePush reads the body. A body that is not JSON, that names no
// ref, or that names no repository is not a push.
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
