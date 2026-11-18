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

// jcatFor builds a jcat file from the jcat fixture, with file as its
// item and digest as its SHA-256. It appends bytes after the gzip
// stream, as the LVFS server sometimes does.
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

// server is a fake HTTP transport that returns the body for each URL in
// the map and 404 for any other URL.
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

// tool is a fake fwupdtool. It returns the output stored for each
// subcommand and records the arguments of every call. An effect runs
// with its subcommand, to imitate a change that the subcommand makes,
// such as an install that writes to the variable store.
type tool struct {
	outputs map[string]toolOutput
	effects map[string]func()
	calls   [][]string
}

type toolOutput struct {
	stdout string
	stderr string
	err    error
}

func (f *tool) run(_ context.Context, args ...string) ([]byte, []byte, error) {
	f.calls = append(f.calls, args)
	if effect := f.effects[args[0]]; effect != nil {
		effect()
	}
	a := f.outputs[args[0]]
	return []byte(a.stdout), []byte(a.stderr), a.err
}

var errExit2 = errors.New("exit status 2")

// framework returns the outputs of fwupdtool on a Framework laptop:
// three devices, two of them updatable, and releases for both of those.
func framework(t *testing.T) map[string]toolOutput {
	return map[string]toolOutput{
		"refresh":     {},
		"get-devices": {stdout: string(fixture(t, "devices.json"))},
		"get-updates": {stdout: string(fixture(t, "updates.json"))},
	}
}

// rig is a scanner with fakes: a server for the catalog and its jcat, a
// fake fwupdtool, and a remotes directory with a remote like the one
// that the fwupd package ships.
type rig struct {
	scanner
	tool *tool
	web  server
}

func newRig(t *testing.T) *rig {
	t.Helper()
	remotes := t.TempDir()
	if err := os.WriteFile(filepath.Join(remotes, "lvfs.conf"), fixture(t, "remote.conf"), 0o644); err != nil {
		t.Fatalf("writing lvfs.conf: %v", err)
	}
	r := &rig{
		tool: &tool{outputs: framework(t)},
		web: server{
			testCatalogURL + ".jcat": jcatFor(t, testFile, sum(testCatalog)),
			testFileURL:              testCatalog,
		},
	}
	r.scanner = scanner{
		cfg:    config{name: "lvfs", url: testCatalogURL, file: testFile, sha256: sum(testCatalog)},
		client: &http.Client{Transport: r.web},
		// An empty directory is a BIOS machine, with no EFI variable
		// store.
		firmwareHost: t.TempDir(),
		firmwareCopy: filepath.Join(t.TempDir(), "fw"),
		fwupdtool:    r.tool.run,
		remotesDir:   remotes,
		workDir:      t.TempDir(),
		trustDir:     filepath.Join(t.TempDir(), "absent"),
		pkiDir:       t.TempDir(),
	}
	return r
}
