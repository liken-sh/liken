package promote

import (
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The Job's container runs Main with the flags the operator writes, and
// the operator reads the report it leaves.
func TestMainWritesTheReport(t *testing.T) {
	w := newWorld(t)
	w.expose("M42_0001", lightFrame(frameID))
	w.expose("M42_0002", lightFrame("0199d3a0-0000-7000-8000-000000000002")[:100])
	reportFile := filepath.Join(t.TempDir(), "termination-log")
	var log strings.Builder

	code := Main([]string{"--outbox", w.outbox, "--archive", w.archive, "--path", "astro", "--target", "m42",
		"--cameras", `{"east-main":{"telescope":"east","train":"east-imaging"}}`, "--report", reportFile}, &log)

	var report Report
	raw, _ := os.ReadFile(reportFile)
	waiting := "east-main/east-tonight/m42/M42_0002.fits: Incomplete the file ends inside its header"
	if err := json.Unmarshal(raw, &report); err != nil || code != 0 || report.Promoted != 1 || !strings.Contains(log.String(), waiting) {
		t.Errorf("Main answered %d and the report %s, %v; the log is\n%s", code, raw, err, log.String())
	}
	lines, _ := w.catalogLines()
	if lines[frameID].Telescope != "east" || lines[frameID].Train != "east-imaging" {
		t.Errorf("the line is %+v", lines[frameID])
	}
}

func TestMainExitCodes(t *testing.T) {
	blockedCatalog := func(w *world) {
		w.write(filepath.Join(w.archive, "astro", "catalog", "m42.jsonl", "x"), nil)
	}
	cases := []struct {
		name    string
		prepare func(*world)
		args    func(w *world, report string) []string
		want    int
	}{
		{"an unknown flag", nil, func(*world, string) []string { return []string{"--archives", "x"} }, 2},
		{"no target", nil, func(_ *world, report string) []string { return []string{"--report", report} }, 2},
		{"cameras that are not JSON", nil,
			func(_ *world, report string) []string {
				return []string{"--target", "m42", "--cameras", "{", "--report", report}
			}, 2},
		{"a catalog that cannot be read", blockedCatalog,
			func(w *world, report string) []string {
				return []string{"--outbox", w.outbox, "--archive", w.archive, "--path", "astro", "--target", "m42", "--report", report}
			}, 1},
		{"a report that cannot be written", nil,
			func(w *world, _ string) []string {
				return []string{"--outbox", w.outbox, "--archive", w.archive, "--target", "m42", "--report", filepath.Join(w.outbox, "no", "dir")}
			}, 1},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			w := newWorld(t)
			if c.prepare != nil {
				c.prepare(w)
			}
			if got := Main(c.args(w, filepath.Join(t.TempDir(), "report")), io.Discard); got != c.want {
				t.Errorf("Main answered %d, want %d", got, c.want)
			}
		})
	}
}
