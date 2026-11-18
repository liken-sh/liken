package main

// kubectl-liken-bluetooth is the CLI plugin of the bluetooth operator.
// kubectl finds it on PATH, so "kubectl liken bluetooth pair" runs
// this binary with the argument "pair". It finds the cluster from
// --kubeconfig and --context, as kubectl does.

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"

	"github.com/spf13/pflag"
	"k8s.io/cli-runtime/pkg/genericclioptions"
)

// version is the release this binary was built from. The build sets it
// with -ldflags "-X main.version=...", as it does for the operator
// image. A binary built any other way reports "dev".
var version = "dev"

const (
	usageText = `kubectl liken bluetooth: pair and unpair Bluetooth devices

Verbs:
  pair               open a pairing window and approve a device you pick
  unpair <device>    remove a paired device
  completion bash    print the bash completion script

Example:
  kubectl liken bluetooth pair`
	adapterUsage    = "the adapter to open the window on"
	windowUsage     = "how long the window stays open"
	forceUsage      = "run despite a version mismatch"
	versionUsage    = "print the version"
	kubeconfigUsage = "path to the kubeconfig file"
	contextUsage    = "the kubeconfig context to use"
	namespaceUsage  = "the namespace to open the window in"
)

func main() {
	if err := run(context.Background(), os.Args[1:], os.Stdin, os.Stdout, os.Stderr); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

// run parses the flags and runs the verb. Tests call it with their own
// arguments and streams.
func run(ctx context.Context, args []string, stdin io.Reader, stdout, stderr io.Writer) error {
	// The shell sends a completion request as __complete followed by a
	// partial flag and a trailing empty word. The flag parser would
	// reject those, so this handles the request before parsing.
	if len(args) > 0 && args[0] == "__complete" {
		return runComplete(ctx, args[1:], stdout)
	}

	flags := pflag.NewFlagSet("kubectl-liken-bluetooth", pflag.ContinueOnError)
	flags.SetOutput(stderr)
	// The help prints the verbs and an example before the flags.
	// pflag's own usage prints only the flags, so this replaces it.
	flags.Usage = func() {}
	printHelp := func() {
		fmt.Fprintln(stderr, usageText)
		fmt.Fprintf(stderr, "\nFlags:\n%s", flags.FlagUsages())
	}

	showVersion := flags.Bool("version", false, versionUsage)
	adapter := flags.String("adapter", "", adapterUsage)
	window := flags.Int("window", 0, windowUsage)
	force := flags.Bool("force", false, forceUsage)

	// cli-runtime builds the REST config from these flags. Only
	// kubeconfig, context, and namespace are bound, which keeps the
	// help short.
	config := genericclioptions.NewConfigFlags(true)
	flags.StringVar(config.KubeConfig, "kubeconfig", *config.KubeConfig, kubeconfigUsage)
	flags.StringVar(config.Context, "context", *config.Context, contextUsage)
	flags.StringVarP(config.Namespace, "namespace", "n", *config.Namespace, namespaceUsage)

	if err := flags.Parse(args); err != nil {
		// --help exits successfully after it prints the usage.
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
	case "pair":
		return runPair(ctx, config, pairOptions{
			Namespace: *config.Namespace,
			Adapter:   pick(*adapter, argAt(positional, 1)),
			Window:    *window,
			Force:     *force,
		}, stdin, stdout, stderr)
	case "unpair":
		return runUnpair(ctx, config, unpairOptions{Device: argAt(positional, 1)}, stderr)
	case "completion":
		return completionScript(argAt(positional, 1), stdout)
	default:
		return fmt.Errorf("unknown verb %q, want pair, unpair, or completion", positional[0])
	}
}

// argAt returns the positional argument at index, or an empty string
// when there is none.
func argAt(positional []string, index int) string {
	if index < len(positional) {
		return positional[index]
	}
	return ""
}

// pick returns first, or second when first is empty. The caller
// passes the flag first, so a flag overrides a positional argument.
func pick(first, second string) string {
	if first != "" {
		return first
	}
	return second
}
