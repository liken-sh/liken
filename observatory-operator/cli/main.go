package main

// Kubectl-liken-observatory is the observatory operator's CLI plugin.
// kubectl finds it on PATH and runs "kubectl liken observatory set-up
// telescope east" as this binary with "set-up telescope east". It owns
// the observatory verbs and takes the cluster's identity from the
// standard kube flags.

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/spf13/pflag"
	"k8s.io/cli-runtime/pkg/genericclioptions"
)

// The release this binary was built from; the build
// stamps it with -ldflags "-X main.version=...", the same way the
// operator image is stamped, and every other build reports dev.
var version = "dev"

// The usage block and the one-line flag descriptions.
const (
	usageText = `kubectl liken observatory: say that a person set equipment up

Verbs:
  set-up <kind> <name>     confirm that you set the resource up for the night
  take-down <kind> <name>  withdraw the confirmation, once you cover it
  completion bash          print the bash completion script

The confirmation is the SetUp condition in the resource's status. It
lasts until the next astronomical dawn at the resource's Observatory,
or until --until.

Example:
  kubectl liken observatory set-up telescope east -n backyard`
	untilUsage      = "when the confirmation expires, as a time such as 2026-10-13T04:30:00Z or a duration from now such as 6h"
	byUsage         = "the person to name in the condition, in place of the user the cluster reports"
	forceUsage      = "run despite a version mismatch"
	versionUsage    = "print the version"
	kubeconfigUsage = "path to the kubeconfig file"
	contextUsage    = "the kubeconfig context to use"
	namespaceUsage  = "the namespace of the resource"
)

func main() {
	if err := run(context.Background(), os.Args[1:], os.Stdout, os.Stderr); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

// run parses the flags and dispatches the verb; it is
// the whole of the binary's behavior, so a test drives it with its own
// arguments and writers.
func run(ctx context.Context, args []string, stdout, stderr io.Writer) error {
	// The __complete verb answers the shell's completion request;
	// it runs before the flag parse, because a request carries a partial
	// flag and a trailing empty word the parser would reject.
	if len(args) > 0 && args[0] == "__complete" {
		return runComplete(ctx, args[1:], stdout)
	}

	flags := pflag.NewFlagSet("kubectl-liken-observatory", pflag.ContinueOnError)
	flags.SetOutput(stderr)
	// The help prints the verbs and an example first, then the flags,
	// so a reader learns what the command does before how to call it.
	// pflag's own usage prints the flags alone, so it is silenced here.
	flags.Usage = func() {}
	printHelp := func() {
		fmt.Fprintln(stderr, usageText)
		fmt.Fprintf(stderr, "\nFlags:\n%s", flags.FlagUsages())
	}

	showVersion := flags.Bool("version", false, versionUsage)
	until := flags.String("until", "", untilUsage)
	by := flags.String("by", "", byUsage)
	force := flags.Bool("force", false, forceUsage)

	// The standard kube flags come from cli-runtime,
	// and this getter turns them into a REST config; context,
	// kubeconfig, and namespace are bound, so the help stays short.
	config := genericclioptions.NewConfigFlags(true)
	flags.StringVar(config.KubeConfig, "kubeconfig", *config.KubeConfig, kubeconfigUsage)
	flags.StringVar(config.Context, "context", *config.Context, contextUsage)
	flags.StringVarP(config.Namespace, "namespace", "n", *config.Namespace, namespaceUsage)

	if err := flags.Parse(args); err != nil {
		// --help is not a failure. printHelp writes the usage, so the
		// branch returns no error.
		if errors.Is(err, pflag.ErrHelp) {
			printHelp()
			return nil
		}
		return err
	}

	if *showVersion {
		fmt.Fprintln(stdout, version)
		return nil
	}

	positional := flags.Args()
	if len(positional) == 0 {
		printHelp()
		return nil
	}

	switch positional[0] {
	case setUpVerb, takeDownVerb:
		return runSetUp(ctx, config, setUpOptions{
			Up:    positional[0] == setUpVerb,
			Kind:  argAt(positional, 1),
			Name:  argAt(positional, 2),
			Until: *until,
			By:    *by,
			Force: *force,
			Now:   time.Now(),
		}, stdout, stderr)
	case "completion":
		return completionScript(argAt(positional, 1), stdout)
	default:
		return fmt.Errorf("unknown verb %q", positional[0])
	}
}

// argAt returns the positional argument at index, or an
// empty string when the caller gave none.
func argAt(positional []string, index int) string {
	if index < len(positional) {
		return positional[index]
	}
	return ""
}
