package main

// kubectl-liken-observatory is the observatory operator's kubectl
// plugin. kubectl finds it on PATH, so "kubectl liken observatory
// set-up telescope east" runs this binary with the arguments "set-up
// telescope east". It selects the cluster with --kubeconfig, --context,
// and --namespace.

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

// version is the release this binary was built from. The build sets it
// with -ldflags "-X main.version=...", and any other build reports dev.
var version = "dev"

const (
	usageText = `kubectl liken observatory: record that a person set equipment up

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

// run is the whole program apart from main, so a test runs it with its
// own arguments and writers.
func run(ctx context.Context, args []string, stdout, stderr io.Writer) error {
	// A completion request can end in a partial flag or an empty word,
	// which the flag parser rejects, so it is handled before the parse.
	if len(args) > 0 && args[0] == "__complete" {
		return runComplete(ctx, args[1:], stdout)
	}

	flags := pflag.NewFlagSet("kubectl-liken-observatory", pflag.ContinueOnError)
	flags.SetOutput(stderr)
	// printHelp prints the verbs and the example before the flags.
	// pflag's default usage prints only the flags, so it is silenced.
	flags.Usage = func() {}
	printHelp := func() {
		fmt.Fprintln(stderr, usageText)
		fmt.Fprintf(stderr, "\nFlags:\n%s", flags.FlagUsages())
	}

	showVersion := flags.Bool("version", false, versionUsage)
	until := flags.String("until", "", untilUsage)
	by := flags.String("by", "", byUsage)
	force := flags.Bool("force", false, forceUsage)

	// cli-runtime's config flags build the REST config. Only the
	// kubeconfig, context, and namespace flags are bound to the flag set,
	// to keep the help short.
	config := genericclioptions.NewConfigFlags(true)
	flags.StringVar(config.KubeConfig, "kubeconfig", *config.KubeConfig, kubeconfigUsage)
	flags.StringVar(config.Context, "context", *config.Context, contextUsage)
	flags.StringVarP(config.Namespace, "namespace", "n", *config.Namespace, namespaceUsage)

	if err := flags.Parse(args); err != nil {
		// --help prints the help and exits with success.
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
		return fmt.Errorf("unknown verb %q, want %s, %s, or completion", positional[0], setUpVerb, takeDownVerb)
	}
}

func argAt(positional []string, index int) string {
	if index < len(positional) {
		return positional[index]
	}
	return ""
}
