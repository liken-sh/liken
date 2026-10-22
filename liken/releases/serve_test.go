package releases

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
	release := filepath.Join(dir, "2026.07.11-001")
	if err := os.Mkdir(release, 0o755); err != nil {
		t.Fatal(err)
	}
	doc := []byte("kind: Release\nmetadata: {name: 2026.07.11-001}\n")
	if err := os.WriteFile(filepath.Join(release, "release.yaml"), doc, 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}

func TestServesPublishedArtifacts(t *testing.T) {
	server := httptest.NewServer(handler(publishedRelease(t), ""))
	defer server.Close()

	resp, err := http.Get(server.URL + "/releases/2026.07.11-001/release.yaml")
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
	if string(body) != "kind: Release\nmetadata: {name: 2026.07.11-001}\n" {
		t.Errorf("body: %q", body)
	}
}

func TestAnswers404ForUnpublishedReleases(t *testing.T) {
	server := httptest.NewServer(handler(publishedRelease(t), ""))
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

// publishedCatalog is a directory of fwupd firmware files, the shape
// that dev-cluster/firmware/capsule/catalog.sh writes.
func publishedCatalog(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "firmware.xml.zst.jcat"), []byte("jcat"), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}

func get(t *testing.T, url string) (int, string) {
	t.Helper()
	resp, err := http.Get(url)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	return resp.StatusCode, string(body)
}

// The firmware directory is served beside the channel, under its own
// path, and the channel's paths stay where they were.
func TestServesFirmwareBesideTheChannel(t *testing.T) {
	server := httptest.NewServer(handler(publishedRelease(t), publishedCatalog(t)))
	defer server.Close()

	cases := []struct {
		path   string
		status int
		body   string
	}{
		{"/firmware/firmware.xml.zst.jcat", http.StatusOK, "jcat"},
		{"/releases/2026.07.11-001/release.yaml", http.StatusOK, "kind: Release\nmetadata: {name: 2026.07.11-001}\n"},
		{"/releases/firmware.xml.zst.jcat", http.StatusNotFound, ""},
	}
	for _, c := range cases {
		t.Run(c.path, func(t *testing.T) {
			status, body := get(t, server.URL+c.path)
			if status != c.status {
				t.Fatalf("status: %d, want %d", status, c.status)
			}
			if c.status == http.StatusOK && body != c.body {
				t.Errorf("body: %q, want %q", body, c.body)
			}
		})
	}
}

func TestServesNoFirmwareWithoutADirectory(t *testing.T) {
	server := httptest.NewServer(handler(publishedRelease(t), ""))
	defer server.Close()

	if status, _ := get(t, server.URL+"/firmware/firmware.xml.zst.jcat"); status != http.StatusNotFound {
		t.Errorf("status: %d", status)
	}
}

// The banner documents the QEMU contract: guests reach the host's
// loopback at 10.0.2.2, so the hint must carry whatever port the
// server actually listens on, and must not assume the default.
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
