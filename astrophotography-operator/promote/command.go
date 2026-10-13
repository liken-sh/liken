package promote

// The command line of the promote mode. The operator writes it into the
// promotion Job's container: `astrophotography-operator promote` and
// the flags below.

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"

	"github.com/liken-sh/liken/astrophotography-operator/outbox"
)

// ArchiveRoot is where the Job mounts the archive claim. With one claim
// for both, the Job mounts it once, at outbox.Root.
const ArchiveRoot = "/archive"

// Main runs the promote mode with the arguments after the mode's name,
// writes the report to the termination message, and answers the exit
// code. A run that reached every frame exits 0, even when frames
// failed, because the report holds each failure and the Job must not
// run again for them. A run that stopped exits 1, and the Job's next
// pod runs it again.
func Main(args []string, log io.Writer) int {
	flags := flag.NewFlagSet("promote", flag.ContinueOnError)
	flags.SetOutput(log)
	var o Options
	var cameras, report string
	flags.StringVar(&o.Outbox, "outbox", outbox.Root, "where the outbox claim is mounted")
	flags.StringVar(&o.Archive, "archive", ArchiveRoot, "where the archive claim is mounted")
	flags.BoolVar(&o.SameClaim, "same-claim", false, "the archive is the outbox's claim, so frames move by rename")
	flags.StringVar(&o.Path, "path", "", "the target's archive.path")
	flags.StringVar(&o.Target, "target", "", "the Target's name")
	flags.StringVar(&cameras, "cameras", "{}", "each camera's telescope and train, as JSON by the camera's name")
	flags.StringVar(&report, "report", TerminationLog, "where to write the report")
	if err := flags.Parse(args); err != nil {
		return 2
	}
	if err := json.Unmarshal([]byte(cameras), &o.Cameras); err != nil || o.Target == "" {
		fmt.Fprintf(log, "promote: --target is required, and --cameras must be JSON: %v\n", err)
		return 2
	}
	result, err := Run(o)
	for _, frame := range append(result.Waiting, result.Failures...) {
		fmt.Fprintf(log, "promote: %s: %s %s\n", frame.File, frame.Reason, frame.Message)
	}
	fmt.Fprintf(log, "promote: %d frames, %d bytes, %d left in the outbox\n", result.Promoted, result.Bytes, result.Left)
	if err != nil {
		fmt.Fprintf(log, "promote: %v\n", err)
		return 1
	}
	// A Report holds strings and numbers only, so it always encodes.
	encoded, _ := json.Marshal(result.Fit())
	if err := os.WriteFile(report, encoded, 0o644); err != nil {
		fmt.Fprintf(log, "promote: writing the report: %v\n", err)
		return 1
	}
	return 0
}
