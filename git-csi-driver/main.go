// git-csi-driver is the CSI driver named git.liken.sh. It mounts git
// repositories as volumes.
package main

import (
	"context"
	"os"
)

func main() {
	os.Exit(run(context.Background(), os.Args[1:], os.Stdout))
}
