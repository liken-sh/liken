package main

import (
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// site writes a built tree with a front page and its Markdown version,
// one component's manual under its own prefix, and a 404 page.
func site(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	files := map[string]string{
		"index.html":         "<title>liken</title>",
		"index.md":           "# liken",
		"404.html":           "<title>Not found</title>",
		"display/index.html": "<title>display-operator</title>",
	}
	for name, body := range files {
		path := filepath.Join(root, name)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatalf("creating the directory of %s: %v", name, err)
		}
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatalf("writing %s: %v", name, err)
		}
	}
	return root
}

func get(t *testing.T, root, path string) *http.Response {
	t.Helper()
	recorder := httptest.NewRecorder()
	handler(root).ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, path, nil))
	return recorder.Result()
}

func body(t *testing.T, response *http.Response) string {
	t.Helper()
	raw, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatalf("reading the response body: %v", err)
	}
	return string(raw)
}

func TestServesTheFrontPage(t *testing.T) {
	response := get(t, site(t), "/")
	if response.StatusCode != http.StatusOK || !strings.Contains(body(t, response), "<title>liken</title>") {
		t.Fatalf("the front page returns status %d, want 200 and the site's title", response.StatusCode)
	}
}

func TestServesAComponentManualUnderItsPrefix(t *testing.T) {
	response := get(t, site(t), "/display/")
	if !strings.Contains(body(t, response), "<title>display-operator</title>") {
		t.Fatalf("/display/ returns status %d, want 200 and the display-operator title", response.StatusCode)
	}
}

func TestServesTheMarkdownVersionAsMarkdown(t *testing.T) {
	response := get(t, site(t), "/index.md")
	if got := response.Header.Get("Content-Type"); !strings.HasPrefix(got, "text/markdown") {
		t.Fatalf("the Content-Type is %q, want text/markdown", got)
	}
}

func TestMissingPageGets404WithTheSitesNotFoundPage(t *testing.T) {
	response := get(t, site(t), "/no/such/page/")
	if response.StatusCode != http.StatusNotFound {
		t.Fatalf("the status is %d, want 404", response.StatusCode)
	}
	if !strings.Contains(body(t, response), "<title>Not found</title>") {
		t.Fatal("the body is not the site's 404.html, want it")
	}
}

// A request for a file beside the tree, above its root, gets a 404.
func TestPathAboveTheTreeGets404WithTheSitesNotFoundPage(t *testing.T) {
	root := site(t)
	outside := filepath.Join(filepath.Dir(root), "outside.html")
	if err := os.WriteFile(outside, []byte("<title>outside</title>"), 0o644); err != nil {
		t.Fatalf("writing the file above the tree: %v", err)
	}
	response := get(t, root, "/../outside.html")
	if response.StatusCode != http.StatusNotFound {
		t.Fatalf("the status is %d, want 404", response.StatusCode)
	}
	if got := body(t, response); !strings.Contains(got, "<title>Not found</title>") {
		t.Fatalf("the body is %q, want the site's 404.html and not the file above the tree", got)
	}
}

func TestRefusesACommandLineWithNoDirectory(t *testing.T) {
	if err := run([]string{"-addr", "localhost:0"}); err == nil {
		t.Fatal("run returned no error for a command line with no -dir, want one")
	}
}

func TestMissingPageGetsPlain404WhenTheTreeHasNoNotFoundPage(t *testing.T) {
	response := get(t, t.TempDir(), "/no/such/page/")
	if response.StatusCode != http.StatusNotFound {
		t.Fatalf("the status is %d, want 404", response.StatusCode)
	}
}

func TestRefusesAFlagItDoesNotKnow(t *testing.T) {
	if err := run([]string{"-port", "8080"}); err == nil {
		t.Fatal("run returned no error for the unknown flag -port, want one")
	}
}

func TestReportsAnAddressItCannotListenOn(t *testing.T) {
	if err := run([]string{"-dir", t.TempDir(), "-addr", "localhost:not-a-port"}); err == nil {
		t.Fatal("run returned no error for an address with no port, want one")
	}
}
