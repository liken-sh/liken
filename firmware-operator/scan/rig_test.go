package main

import (
	"bytes"
	"compress/gzip"
	"context"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const (
	testCatalogURL = "https://cdn.example.test/downloads/firmware.xml.zst"
	testFile       = "firmware-09138-stable.xml.zst"
	testFileURL    = "https://cdn.example.test/downloads/" + testFile
)

var testCatalog = []byte("catalog bytes")

func fixture(t *testing.T, name string) []byte {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	return data
}

// jcatFor builds a jcat file that names file and gives digest as its
// SHA-256, from the jcat fixture. The bytes after the gzip stream are
// the trailing garbage that the LVFS server sometimes sends.
func jcatFor(t *testing.T, file, digest string) []byte {
	t.Helper()
	plain := string(fixture(t, "jcat.json"))
	plain = strings.ReplaceAll(plain, "firmware-09138-stable.xml.zst", file)
	plain = strings.ReplaceAll(plain, "SHA256", digest)
	var buf bytes.Buffer
	zw := gzip.NewWriter(&buf)
	zw.Write([]byte(plain))
	zw.Close()
	buf.WriteString("\x00\x00trailing")
	return buf.Bytes()
}

// server answers requests from a table of URL to body, and 404 for any
// other URL. A missing body is how a test makes a download fail.
type server map[string][]byte

func (s server) RoundTrip(req *http.Request) (*http.Response, error) {
	body, ok := s[req.URL.String()]
	status := http.StatusOK
	if !ok {
		status = http.StatusNotFound
	}
	return &http.Response{
		StatusCode: status, Status: http.StatusText(status),
		Body: io.NopCloser(bytes.NewReader(body)), Request: req,
	}, nil
}

// tool is a fake fwupdtool. Each command answers from a table keyed by
// its first argument, and the calls are recorded.
type tool struct {
	answers map[string]answer
	calls   [][]string
}

type answer struct {
	stdout string
	stderr string
	err    error
}

func (f *tool) run(_ context.Context, args ...string) ([]byte, []byte, error) {
	f.calls = append(f.calls, args)
	a := f.answers[args[0]]
	return []byte(a.stdout), []byte(a.stderr), a.err
}

var errExit2 = errors.New("exit status 2")

// framework is the answer of a Framework laptop: three devices, two of
// them updatable, one with a release.
func framework(t *testing.T) map[string]answer {
	return map[string]answer{
		"refresh":     {},
		"get-devices": {stdout: string(fixture(t, "devices.json"))},
		"get-updates": {stdout: string(fixture(t, "updates.json"))},
	}
}

// rig is a scanner with a server that holds the catalog and its jcat,
// the fwupdtool fake, and a remotes directory with one shipped remote.
type rig struct {
	scanner
	tool *tool
	web  server
}

func newRig(t *testing.T) *rig {
	t.Helper()
	remotes := t.TempDir()
	if err := os.WriteFile(filepath.Join(remotes, "lvfs.conf"), fixture(t, "remote.conf"), 0o644); err != nil {
		t.Fatal(err)
	}
	r := &rig{
		tool: &tool{answers: framework(t)},
		web: server{
			testCatalogURL + ".jcat": jcatFor(t, testFile, sum(testCatalog)),
			testFileURL:              testCatalog,
		},
	}
	r.scanner = scanner{
		cfg:        config{name: "lvfs", url: testCatalogURL, file: testFile, sha256: sum(testCatalog)},
		client:     &http.Client{Transport: r.web},
		mounter:    mounter{exists: func(string) bool { return false }},
		fwupdtool:  r.tool.run,
		remotesDir: remotes,
		workDir:    t.TempDir(),
	}
	return r
}
