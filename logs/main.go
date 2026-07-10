// liken-logs relays the machine's host-level log streams into
// Kubernetes, one relay per stream, so that everything below the
// cluster becomes readable through the cluster. liken's kernel,
// init, k3s, and containerd write their logs to the serial console
// and to files on the machine, and none of that is visible to
// `kubectl logs` or to any log stack someone might run: a collector
// can't tail a serial port. Each relay reads one stream at its
// source and re-emits it, line by line, on its own stdout, and
// because each relay runs as a pod, its stdout is a pod log: the
// Kubernetes API serves it, RBAC governs it, and any in-cluster
// collector consumes it with no host privileges. The OS's log
// interface is `kubectl logs`, the same API as everything else.
//
// One binary carries all four relays, chosen by an argument verb
// (the multi-call pattern, the same shape init uses to re-exec
// itself). The machine-logs DaemonSet runs all four as containers of
// one pod, each differing only in its verb and in what it mounts:
//
//	liken-logs kernel      /dev/kmsg, records with syslog facility 0
//	liken-logs liken       /dev/kmsg, records with syslog facility 1
//	liken-logs k3s         the k3s log on clusterState
//	liken-logs containerd  containerd's log on clusterState
//
// The kernel and init share /dev/kmsg (init writes its lines there
// precisely so they interleave with the kernel's in true order), and
// the facility field is what splits them back apart, so each pod
// carries exactly one program's records.
//
// A relay is crash-only: any unexpected error exits nonzero, the
// kubelet restarts the container, and the cursor in the pod's
// emptyDir resumes the stream. There is no retry logic layered on
// top; restarting from a durable position is the retry logic.
package main

import (
	"fmt"
	"os"

	"github.com/liken-sh/liken/machine"
)

// The host paths each verb reads, and the emptyDir where cursors
// live. These are fixed facts of the DaemonSet's mounts; tests point
// a relay at a temporary directory through its parameters instead.
const (
	cursorDir         = "/cursor"
	k3sLogPath        = "/var/lib/rancher/k3s/liken/k3s.log"
	containerdLogPath = "/var/lib/rancher/k3s/agent/containerd/containerd.log"
)

func main() {
	// The version goes to stderr because stdout is the data channel:
	// the relay never writes anything to stdout that isn't an
	// envelope.
	fmt.Fprintln(os.Stderr, "liken-logs", machine.Version)

	if len(os.Args) != 2 {
		usage()
		os.Exit(2)
	}
	var err error
	switch os.Args[1] {
	case "kernel":
		err = relayKmsg(machine.FacilityKernel)
	case "liken":
		err = relayKmsg(machine.FacilityUser)
	case "k3s":
		err = relayFile(k3sLogPath)
	case "containerd":
		err = relayFile(containerdLogPath)
	default:
		usage()
		os.Exit(2)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "liken-logs:", err)
		os.Exit(1)
	}
}

func usage() {
	fmt.Fprintln(os.Stderr, "usage: liken-logs kernel|liken|k3s|containerd")
}
