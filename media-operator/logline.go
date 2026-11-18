package main

// Each operation that a person causes, such as a key press, a playback
// request, a volume step, or an edit to a resource, writes one line to
// standard output. The line says what triggered the operation, what was
// sent, and what came back. Background work, such as position reports
// and passes that change nothing, writes no line, so the log is a record
// of what people did. Failures of the program itself, such as a list
// call that failed, go to standard error.

import (
	"fmt"
	"io"
	"os"
	"strings"
)

// logLine writes one formatted line to out, or to standard output when
// out is nil. A test passes its own writer to read the lines.
func logLine(out io.Writer, format string, args ...any) {
	if out == nil {
		out = os.Stdout
	}
	fmt.Fprintf(out, format+"\n", args...)
}

// remoteOfTopic returns namespace/name from a Remote's topic,
// <base>/remotes/<namespace>/<name>/<kind>. The base can itself contain
// a "remotes" segment, so the search starts at the end. A topic of any
// other shape is returned unchanged, so the log line still shows it.
func remoteOfTopic(topic string) string {
	parts := strings.Split(topic, "/")
	for index := len(parts) - 1; index >= 0; index-- {
		if parts[index] == "remotes" && index+2 < len(parts) {
			return parts[index+1] + "/" + parts[index+2]
		}
	}
	return topic
}
