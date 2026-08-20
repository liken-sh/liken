// The media operator reconciles Player and Play resources into
// playback pods. Plan 01 (plans/01-a-play-becomes-a-pod.md) builds
// that loop; this program is the scaffold it lands in, and it
// refuses to run so that nobody mistakes a deployed scaffold for a
// working operator.
package main

import (
	"fmt"
	"os"
)

func main() {
	fmt.Fprintln(os.Stderr,
		"the media operator is not built yet; see plans/01-a-play-becomes-a-pod.md")
	os.Exit(1)
}
