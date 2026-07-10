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

// The banner documents the QEMU contract: guests reach the host's
// loopback at 10.0.2.2, so the hint must carry whatever port the
// server actually listens on, not assume the default.
func TestBannerDerivesTheGuestURLFromTheAddress(t *testing.T) {
	cases := []struct {
		name string
		addr string
		want string
	}{
		{
			name: "the default port",
			addr: ":8017",
			want: "serving releases from dist on :8017 (guests reach this at http://10.0.2.2:8017/releases)",
		},
		{
			name: "a custom port",
			addr: "0.0.0.0:9000",
			want: "serving releases from dist on 0.0.0.0:9000 (guests reach this at http://10.0.2.2:9000/releases)",
		},
		{
			name: "an address without a port gets no guest hint",
			addr: "localhost",
			want: "serving releases from dist on localhost",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := banner("dist", c.addr); got != c.want {
				t.Errorf("banner:\ngot  %q\nwant %q", got, c.want)
			}
		})
	}
}
