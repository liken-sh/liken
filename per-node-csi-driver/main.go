// per-node-csi-driver is the CSI driver named per-node.liken.sh. It
// gives a pod a directory on the node it runs on. The directory stays
// on that node when the pod leaves, and a pod on another node gets a
// directory of its own there.
package main

import (
	"context"
	"os"
)

func main() {
	os.Exit(run(context.Background(), os.Args[1:], os.Stdout))
}
