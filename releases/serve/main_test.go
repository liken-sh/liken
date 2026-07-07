package main

import (
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func publishedRelease(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	release := filepath.Join(dir, "0.1.0")
	if err := os.Mkdir(release, 0o755); err != nil {
		t.Fatal(err)
	}
	doc := []byte("kind: Release\nmetadata: {name: 0.1.0}\n")
	if err := os.WriteFile(filepath.Join(release, "release.yaml"), doc, 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}

func TestServesPublishedArtifacts(t *testing.T) {
	server := httptest.NewServer(handler(publishedRelease(t)))
	defer server.Close()

	resp, err := http.Get(server.URL + "/releases/0.1.0/release.yaml")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status: %d", resp.StatusCode)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	if string(body) != "kind: Release\nmetadata: {name: 0.1.0}\n" {
		t.Errorf("body: %q", body)
	}
}

func TestAnswers404ForUnpublishedReleases(t *testing.T) {
	server := httptest.NewServer(handler(publishedRelease(t)))
	defer server.Close()

	resp, err := http.Get(server.URL + "/releases/9.9.9/release.yaml")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("status: %d", resp.StatusCode)
	}
}
