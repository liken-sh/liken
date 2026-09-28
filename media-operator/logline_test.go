package main

import (
	"bytes"
	"strings"
	"sync"
	"testing"
)

// logBuffer collects the lines a role prints. The roles write from more
// than one goroutine, so the buffer takes a lock, and the race detector
// reads a test's writer the way it reads the role's own state.
type logBuffer struct {
	mutex  sync.Mutex
	buffer bytes.Buffer
}

func (b *logBuffer) Write(p []byte) (int, error) {
	b.mutex.Lock()
	defer b.mutex.Unlock()
	return b.buffer.Write(p)
}

// lines returns every line written so far, without the final newline.
func (b *logBuffer) lines() []string {
	b.mutex.Lock()
	defer b.mutex.Unlock()
	text := strings.TrimSuffix(b.buffer.String(), "\n")
	if text == "" {
		return nil
	}
	return strings.Split(text, "\n")
}

// mustLogOnce asserts that exactly one line was written, and that it
// carries each fact.
func mustLogOnce(t *testing.T, log *logBuffer, facts ...string) {
	t.Helper()
	lines := log.lines()
	if len(lines) != 1 {
		t.Fatalf("wanted one line, got %d: %q", len(lines), lines)
	}
	for _, fact := range facts {
		if !strings.Contains(lines[0], fact) {
			t.Errorf("the line %q does not say %q", lines[0], fact)
		}
	}
}

// mustLogNothing asserts that no line was written.
func mustLogNothing(t *testing.T, log *logBuffer) {
	t.Helper()
	if lines := log.lines(); len(lines) != 0 {
		t.Errorf("wanted no line, got %q", lines)
	}
}

func TestALineGoesToTheWriterTheRoleHolds(t *testing.T) {
	var log logBuffer

	logLine(&log, "remote: %s pressed", "KEY_UP")

	mustMatchAll(t, log.lines(), []string{"remote: KEY_UP pressed"})
}

func TestRemoteOfTopicNamesTheRemoteAControllerTopicBelongsTo(t *testing.T) {
	cases := []struct {
		topic string
		want  string
	}{
		{topic: remoteEventsTopic(defaultTopicBase, "den", "tv-remote"), want: "den/tv-remote"},
		{topic: remoteFocusTopic(defaultTopicBase, "den", "pad"), want: "den/pad"},
		{topic: remoteFocusCycleTopic("a/remotes/b", "den", "pad"), want: "den/pad"},
		{topic: "somewhere/else", want: "somewhere/else"},
	}
	for _, each := range cases {
		t.Run(each.topic, func(t *testing.T) {
			mustMatch(t, remoteOfTopic(each.topic), each.want)
		})
	}
}
