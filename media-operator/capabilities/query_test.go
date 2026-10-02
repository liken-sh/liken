package main

import (
	"bytes"
	"context"
	"os/exec"
	"strings"
	"testing"
	"time"
)

// shellQuerier runs a shell script in place of the query, so a test
// states what the child writes and how it ends.
func shellQuerier(script string, timeout time.Duration) *querier {
	return &querier{
		command: func(ctx context.Context, path string) *exec.Cmd {
			return exec.CommandContext(ctx, "/bin/sh", "-c", script, "query", path)
		},
		timeout: timeout,
	}
}

func TestAQueryThatAnswersReturnsItsReport(t *testing.T) {
	q := shellQuerier(`echo '{"vendor":"iHD","configs":[{"profile":18,"entrypoint":1,"rtFormat":256}],"videoProcFormats":["P010"]}'`, time.Minute)
	got, err := q.query(context.Background(), "/dev/dri/renderD128")
	if err != nil {
		t.Fatal(err)
	}
	if got.Vendor != "iHD" || len(got.Configs) != 1 || got.Configs[0].RTFormat != rtFormatYUV420_10 {
		t.Errorf("report = %+v", got)
	}
}

func TestAQueryThatFailsReturnsItsStderr(t *testing.T) {
	q := shellQuerier(`echo "vaInitialize on $1: unknown libva error" >&2; exit 1`, time.Minute)
	_, err := q.query(context.Background(), "/dev/dri/renderD128")
	if err == nil || !strings.Contains(err.Error(), "vaInitialize on /dev/dri/renderD128: unknown libva error") {
		t.Errorf("err = %v, want the child's stderr word for word", err)
	}
}

func TestAQueryThatHangsIsKilledAtTheTimeout(t *testing.T) {
	q := shellQuerier(`echo "libva info: loading the driver" >&2; exec sleep 60`, 100*time.Millisecond)
	start := time.Now()
	_, err := q.query(context.Background(), "/dev/dri/renderD128")
	if err == nil || !strings.Contains(err.Error(), "ran past 100ms") ||
		!strings.Contains(err.Error(), "libva info: loading the driver") {
		t.Errorf("err = %v, want a timeout that holds what the child wrote", err)
	}
	if elapsed := time.Since(start); elapsed > 10*time.Second {
		t.Errorf("the query took %s, want it killed at the timeout", elapsed)
	}
}

func TestAQueryThatWritesNoJSONFails(t *testing.T) {
	q := shellQuerier(`echo "not a report"`, time.Minute)
	_, err := q.query(context.Background(), "/dev/dri/renderD128")
	if err == nil || !strings.Contains(err.Error(), "does not decode") {
		t.Errorf("err = %v", err)
	}
}

func TestTheChildRefusesAQueryWithNoRenderNode(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if code := runQuery(nil, &stdout, &stderr); code != 2 || !strings.Contains(stderr.String(), "usage") {
		t.Errorf("code = %d, stderr = %q", code, stderr.String())
	}
}

func TestTheChildReportsLibvasFailureAndExitsNonzero(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if code := runQuery([]string{"/dev/null"}, &stdout, &stderr); code != 1 || !strings.Contains(stderr.String(), "/dev/null") {
		t.Errorf("code = %d, stderr = %q", code, stderr.String())
	}
}
