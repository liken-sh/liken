package main

// request-reboot is approve-reboot's counterpart. Every other reboot
// liken performs applies a staged change, so a machine that agrees
// with every document it was given has no way to reboot, and a
// machine with no shell has no other way to be told. This command is
// how a person asks: it reads status.boot.time, derives the running
// boot's identity, and files a Disruption named for it
// (disruptions.go). Naming the running boot makes the request
// one-shot: machine-operator marks it Complete when the machine runs
// another boot, so a later request works with no cleanup in between.
//
// The request skips no coordination. The machine still takes its
// turn from the cluster's disruption budget, cordons, and drains. On
// rebootPolicy: Manual it parks where a staged change parks, and
// liken approve-reboot releases it.

import (
	"flag"
	"fmt"
	"io"
	"strings"

	"github.com/liken-sh/liken/liken/kubernetes"
	"github.com/liken-sh/liken/liken/machine"
)

// renderRequest reports what the request set in motion. What happens
// next depends on the machine's rebootPolicy, so the report says
// which of the two paths this machine is on, and names the command
// that finishes the second one.
func renderRequest(m *machine.Machine, dir, filed string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "created: Disruption %s\n", filed)
	if m.Spec.RebootPolicyOrDefault() == machine.RebootAuto {
		fmt.Fprintf(&b, "%s waits for a reboot turn from the cluster, drains its workloads, and reboots.\n", m.Metadata.Name)
	} else {
		fmt.Fprintf(&b, "%s has rebootPolicy: Manual, so the request waits for you. Approve it with:\n", m.Metadata.Name)
		fmt.Fprintf(&b, "  liken approve-reboot %s %s\n", dir, m.Metadata.Name)
	}
	staged := stagedChanges(m.Status.Pending)
	if len(staged) == 0 {
		fmt.Fprint(&b, "No change is staged, so the machine boots the same release and configuration.\n")
		return b.String()
	}
	fmt.Fprint(&b, "The reboot also applies every staged change:\n")
	for _, p := range staged {
		fmt.Fprintf(&b, "  %s (%.12s)\n", p.Summary, p.Hash)
	}
	return b.String()
}

// stagedChanges leaves out the entries for reboots that a person
// requested, because those entries have no document for the reboot to
// apply.
func stagedChanges(pending []machine.PendingDisruption) []machine.PendingDisruption {
	var staged []machine.PendingDisruption
	for _, p := range pending {
		if p.Condition != rebootRequestCondition {
			staged = append(staged, p)
		}
	}
	return staged
}

// requestReboot is the command: resolve the credential, read the
// machine, name its boot, and ask.
func requestReboot(args []string, out io.Writer) error {
	fs := flag.NewFlagSet("request-reboot", flag.ContinueOnError)
	server := fs.String("server", "", "the API server address, when cluster.yaml's endpoint is not reachable from here")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 2 {
		return fmt.Errorf("request-reboot takes a deployment directory and a machine\nusage: liken request-reboot [-server URL] <deployment-dir> <machine>")
	}
	kubeconfigPath, err := writeKubeconfig(fs.Arg(0), *server, io.Discard)
	if err != nil {
		return err
	}
	c, err := kubernetes.KubeconfigClient(kubeconfigPath)
	if err != nil {
		return err
	}
	m, err := kubernetes.GetMachine(c, fs.Arg(1))
	if err != nil {
		return err
	}
	// A machine that reports no boot time has published nothing for
	// the request to name. That is a machine still starting, or one
	// that has never reported at all, and either way there is no
	// boot to ask about yet.
	bootID := machine.BootID(m.Status.Boot)
	if bootID == "" {
		return fmt.Errorf("machine %s has not reported its boot time in status.boot.time, so liken cannot request a reboot. The machine is still starting, or it has never reported its status", m.Metadata.Name)
	}
	filed, err := fileRebootRequest(c, m, bootID)
	if err != nil {
		return fmt.Errorf("filing the Disruption: %w", err)
	}
	if filed == "" {
		return errNoDisruptions
	}
	fmt.Fprint(out, renderRequest(m, fs.Arg(0), filed))
	return nil
}
