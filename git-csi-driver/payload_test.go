package main

import (
	"slices"
	"testing"
)

func TestParsePushReadsEachForgesBody(t *testing.T) {
	for _, c := range []struct {
		name string
		body string
		ref  string
		urls []string
	}{
		{
			name: "github",
			body: `{"ref":"refs/heads/main","repository":{
				"clone_url":"https://github.com/o/r.git",
				"ssh_url":"git@github.com:o/r.git",
				"git_url":"git://github.com/o/r.git",
				"html_url":"https://github.com/o/r"}}`,
			ref: "refs/heads/main",
			urls: []string{
				"https://github.com/o/r.git",
				"git@github.com:o/r.git",
				"git://github.com/o/r.git",
				"https://github.com/o/r",
			},
		},
		{
			name: "gitlab",
			body: `{"ref":"refs/heads/main","project":{
				"git_http_url":"https://gitlab.com/o/r.git",
				"git_ssh_url":"git@gitlab.com:o/r.git",
				"web_url":"https://gitlab.com/o/r"},
				"repository":{"url":"git@gitlab.com:o/r.git"}}`,
			ref: "refs/heads/main",
			urls: []string{
				"git@gitlab.com:o/r.git",
				"https://gitlab.com/o/r.git",
				"git@gitlab.com:o/r.git",
				"https://gitlab.com/o/r",
			},
		},
		{
			name: "gitea and forgejo",
			body: `{"ref":"refs/tags/v1","repository":{
				"clone_url":"https://code.example.com/o/r.git",
				"ssh_url":"git@code.example.com:o/r.git",
				"html_url":"https://code.example.com/o/r"}}`,
			ref: "refs/tags/v1",
			urls: []string{
				"https://code.example.com/o/r.git",
				"git@code.example.com:o/r.git",
				"https://code.example.com/o/r",
			},
		},
	} {
		t.Run(c.name, func(t *testing.T) {
			pushed, isPush := parsePush([]byte(c.body))
			if !isPush {
				t.Fatalf("parsePush read no push out of %s", c.body)
			}
			if pushed.ref != c.ref {
				t.Errorf("parsePush read the ref %q, want %q", pushed.ref, c.ref)
			}
			if !slices.Equal(pushed.urls, c.urls) {
				t.Errorf("parsePush read the URLs %q, want %q", pushed.urls, c.urls)
			}
		})
	}
}

func TestParsePushRefusesABodyThatIsNoPush(t *testing.T) {
	for _, c := range []struct {
		name string
		body string
	}{
		{name: "not JSON", body: "this is not JSON"},
		{name: "no ref", body: `{"repository":{"clone_url":"https://example.com/r.git"}}`},
		{name: "no repository", body: `{"ref":"refs/heads/main"}`},
		{name: "empty", body: ""},
	} {
		t.Run(c.name, func(t *testing.T) {
			if _, isPush := parsePush([]byte(c.body)); isPush {
				t.Errorf("parsePush read a push out of %q", c.body)
			}
		})
	}
}
