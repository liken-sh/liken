package main

// request-reboot is approve-reboot's counterpart. Every other reboot
// liken performs applies a staged change, so a machine that agrees
// with every document it was given has no way to reboot, and a
// machine with no shell has no other way to be told. This command is
// how a person asks: it reads status.boot.time, derives the running
// boot's identity, and writes the request-reboot annotation naming
// it. Naming the running boot makes the request one-shot
// (machine.RequestRebootAnnotation explains why), so a later request
// works with no cleanup in between.
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

	"github.com/liken-sh/liken/kubernetes"
	"github.com/liken-sh/liken/machine"
)

// renderRequest reports what the request set in motion. What happens
// next depends on the machine's rebootPolicy, so the report says
// which of the two paths this machine is on, and names the command
// that finishes the second one.
func renderRequest(m *machine.Machine, dir, short string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "requested: %s=%s\n", machine.RequestRebootAnnotation, short)
	if m.Spec.RebootPolicyOrDefault() == machine.RebootAuto {
		fmt.Fprintf(&b, "%s takes a reboot turn from the cluster, drains its workloads, and reboots.\n", m.Metadata.Name)
	} else {
		fmt.Fprintf(&b, "%s has rebootPolicy: Manual, so the request waits for you. Release it with:\n", m.Metadata.Name)
		fmt.Fprintf(&b, "  liken approve-reboot %s %s\n", dir, m.Metadata.Name)
	}
	fmt.Fprint(&b, "Nothing is staged, so the machine comes back on the documents it runs now.\n")
	return b.String()
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
		return fmt.Errorf("usage: liken request-reboot [-server URL] <deployment-dir> <machine>")
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
		return fmt.Errorf("%s reports no boot time, so there is no boot to request a reboot of; the machine is still starting, or it has never reported", m.Metadata.Name)
	}
	short := fmt.Sprintf("%.12s", bootID)
	patch := fmt.Sprintf(`{"metadata":{"annotations":{%q:%q}}}`, machine.RequestRebootAnnotation, short)
	if err := c.PatchJSON(kubernetes.MachinesPath+"/"+m.Metadata.Name, []byte(patch)); err != nil {
		return err
	}
	fmt.Fprint(out, renderRequest(m, fs.Arg(0), short))
	return nil
}
