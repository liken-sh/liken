package main

// approve-reboot is the one cluster command that carries liken's own
// meaning instead of handing the terminal to another tool. A machine
// whose rebootPolicy is Manual stages a change and waits for a
// person. This command is how the person answers: it reads
// status.pending, reports what is waiting, and writes the
// approve-disruption annotation with the staged document's hash. The
// hash makes the grant one-shot (machine.ApproveDisruptionAnnotation
// explains why), and writing the same annotation twice is the same
// grant, so the command is repeatable.

import (
	"flag"
	"fmt"
	"io"
	"strings"

	"github.com/liken-sh/liken/api"
	"github.com/liken-sh/liken/kubernetes"
	"github.com/liken-sh/liken/machine"
)

// chooseApproval picks which pending entry to approve. A reboot
// applies every staged document at once, so when a reboot-class
// change is pending, approving it covers the rest. Otherwise the
// first entry stands for the list: with several restart-class
// changes pending, each applies on its own grant, and running the
// command again approves the next.
func chooseApproval(pending []machine.PendingDisruption) *machine.PendingDisruption {
	for i := range pending {
		if pending[i].Kind == machine.DisruptionReboot {
			return &pending[i]
		}
	}
	if len(pending) > 0 {
		return &pending[0]
	}
	return nil
}

// renderPending reports what the machine waits for, in the shape a
// person reads before deciding to grant: each change's condition and
// reason, its summary with the short hash a grant names, and what
// kind of disruption applies it.
func renderPending(m *machine.Machine) string {
	name := m.Metadata.Name
	pending := m.Status.Pending
	if len(pending) == 0 {
		return fmt.Sprintf("%s is converged; nothing is waiting\n", name)
	}
	count := fmt.Sprintf("%d changes", len(pending))
	if len(pending) == 1 {
		count = "one change"
	}
	var b strings.Builder
	fmt.Fprintf(&b, "%s is waiting on %s:\n", name, count)
	for _, p := range pending {
		condition := p.Condition
		if c := api.FindCondition(m.Status.Conditions, p.Condition); c != nil {
			condition = fmt.Sprintf("%s  %s", p.Condition, c.Reason)
		}
		applies := "a reboot applies this, and every other staged change with it"
		if p.Kind == machine.DisruptionRestart {
			applies = "a k3s restart applies this; the machine does not reboot"
		}
		fmt.Fprintf(&b, "  %s\n  %s (%.12s)\n  %s\n", condition, p.Summary, p.Hash, applies)
	}
	return b.String()
}

// approveReboot is the command: resolve the credential, read the
// machine, report, and grant.
func approveReboot(args []string, out io.Writer) error {
	fs := flag.NewFlagSet("approve-reboot", flag.ContinueOnError)
	server := fs.String("server", "", "the API server address, when cluster.yaml's endpoint is not reachable from here")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 2 {
		return fmt.Errorf("usage: liken approve-reboot [-server URL] <deployment-dir> <machine>")
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
	fmt.Fprint(out, renderPending(m))
	choice := chooseApproval(m.Status.Pending)
	if choice == nil {
		return nil
	}
	short := fmt.Sprintf("%.12s", choice.Hash)
	patch := fmt.Sprintf(`{"metadata":{"annotations":{%q:%q}}}`, machine.ApproveDisruptionAnnotation, short)
	if err := c.PatchJSON(kubernetes.MachinesPath+"/"+m.Metadata.Name, []byte(patch)); err != nil {
		return err
	}
	fmt.Fprintf(out, "\napproved: %s=%s\n", machine.ApproveDisruptionAnnotation, short)
	return nil
}
