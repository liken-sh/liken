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

// site writes a small built tree: a front page and its Markdown twin,
// one component's manual under its own prefix, and the 404 page.
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
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatal(err)
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
		t.Fatal(err)
	}
	return string(raw)
}

func TestServesTheFrontPage(t *testing.T) {
	response := get(t, site(t), "/")
	if response.StatusCode != http.StatusOK || !strings.Contains(body(t, response), "<title>liken</title>") {
		t.Fatalf("front page: status %d", response.StatusCode)
	}
}

func TestServesAComponentManualUnderItsPrefix(t *testing.T) {
	response := get(t, site(t), "/display/")
	if !strings.Contains(body(t, response), "<title>display-operator</title>") {
		t.Fatalf("display manual: status %d", response.StatusCode)
	}
}

func TestServesTheMarkdownTwinAsMarkdown(t *testing.T) {
	response := get(t, site(t), "/index.md")
	if got := response.Header.Get("Content-Type"); !strings.HasPrefix(got, "text/markdown") {
		t.Fatalf("Content-Type = %q, want text/markdown", got)
	}
}

func TestAnswersAMissingPageWithTheSitesNotFoundPage(t *testing.T) {
	response := get(t, site(t), "/no/such/page/")
	if response.StatusCode != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", response.StatusCode)
	}
	if !strings.Contains(body(t, response), "<title>Not found</title>") {
		t.Fatal("the body is not the site's 404.html")
	}
}

func TestRefusesACommandLineWithNoDirectory(t *testing.T) {
	if err := run([]string{"-addr", "localhost:0"}); err == nil {
		t.Fatal("run accepted a command line with no -dir")
	}
}

func TestAnswersAMissingPageWithAPlainNotFoundWhenTheTreeHasNone(t *testing.T) {
	response := get(t, t.TempDir(), "/no/such/page/")
	if response.StatusCode != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", response.StatusCode)
	}
}

func TestRefusesAFlagItDoesNotKnow(t *testing.T) {
	if err := run([]string{"-port", "8080"}); err == nil {
		t.Fatal("run accepted -port")
	}
}

func TestReportsAnAddressItCannotListenOn(t *testing.T) {
	if err := run([]string{"-dir", t.TempDir(), "-addr", "localhost:not-a-port"}); err == nil {
		t.Fatal("run listened on an address with no port")
	}
}
