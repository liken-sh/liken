package main

// Every operation a person causes gets one line in the log of the role
// that performs it: a press, a playback request, a volume step, a focus
// move, a controller that connects, an edit the operator acts on. The
// line says what triggered the operation, what the role sent, and what
// came back. Loops a person does not see, such as a poll, a position
// report, or a pass that changes nothing, write no line, so the lines
// that remain read as a record of what people did.
//
// The lines go to standard output. Standard error keeps the failures of
// the role's own machinery, such as a list that failed or a socket that
// closed.

import (
	"fmt"
	"io"
	"os"
	"strings"
)

// logLine writes one line to out, or to standard output when out is
// nil. Each role holds its writer as a field, so a test reads the lines
// the role would print, and a role a test builds with no writer prints
// where the pod would.
func logLine(out io.Writer, format string, args ...any) {
	if out == nil {
		out = os.Stdout
	}
	fmt.Fprintf(out, format+"\n", args...)
}

// remoteOfTopic names the Remote a controller topic belongs to, as
// namespace/name. Every controller topic is
// <base>/remotes/<namespace>/<name>/<kind>, and the base can hold
// slashes of its own, so the name is found from the remotes segment and
// not from the start. A topic of another shape names itself, so a line
// never loses its trigger.
func remoteOfTopic(topic string) string {
	parts := strings.Split(topic, "/")
	for index := len(parts) - 1; index >= 0; index-- {
		if parts[index] == "remotes" && index+2 < len(parts) {
			return parts[index+1] + "/" + parts[index+2]
		}
	}
	return topic
}
