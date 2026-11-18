package main

import (
	"bytes"
	"strings"
	"sync"
	"testing"
)

// logBuffer collects the lines that a role prints. A role writes from
// several goroutines, so the buffer takes a lock, and the race detector
// does not flag the test's reads.
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

// mustLogOnce asserts that exactly one line was written and that the
// line contains each fact.
func mustLogOnce(t *testing.T, log *logBuffer, facts ...string) {
	t.Helper()
	lines := log.lines()
	if len(lines) != 1 {
		t.Fatalf("the log has %d lines %q, want one", len(lines), lines)
	}
	for _, fact := range facts {
		if !strings.Contains(lines[0], fact) {
			t.Errorf("the line is %q, want it to contain %q", lines[0], fact)
		}
	}
}

// mustLogNothing asserts that no line was written.
func mustLogNothing(t *testing.T, log *logBuffer) {
	t.Helper()
	if lines := log.lines(); len(lines) != 0 {
		t.Errorf("the log has the lines %q, want none", lines)
	}
}

func TestALineGoesToTheGivenWriter(t *testing.T) {
	var log logBuffer

	logLine(&log, "remote: %s pressed", "KEY_UP")

	mustMatchAll(t, log.lines(), []string{"remote: KEY_UP pressed"})
}

func TestRemoteOfTopicReturnsNamespaceAndNameOfTheTopicsRemote(t *testing.T) {
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
