package main

// kubectl-liken-media is the media operator's kubectl plugin. kubectl
// finds it on PATH, so "kubectl liken media capture living-room" runs
// this binary with the arguments "capture living-room".

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"

	"github.com/spf13/pflag"
	"k8s.io/cli-runtime/pkg/genericclioptions"
)

// version is set by the release build with -ldflags "-X main.version=...".
var version = "dev"

const (
	usageText = `kubectl liken media: capture what a Player is showing

Verbs:
  capture <player>   stream the Player's video and sound to stdout as MP4
  completion bash    print the bash completion script

Example:
  kubectl liken media capture studio -n default > studio.mp4`
	formatUsage     = "mp4 or mkv"
	forceUsage      = "run despite a version mismatch"
	versionUsage    = "print the version"
	kubeconfigUsage = "path to the kubeconfig file"
	contextUsage    = "the kubeconfig context to use"
	namespaceUsage  = "the Player's namespace"
)

func main() {
	if err := run(context.Background(), os.Args[1:], os.Stdout, os.Stderr); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

// run parses the flags and runs the verb. main does nothing else, so a
// test calls run with its own arguments and writers.
func run(ctx context.Context, args []string, stdout, stderr io.Writer) error {
	// A shell completion request is handled before the flag parse,
	// because it ends with a partial flag and an empty word, which the
	// parser rejects.
	if len(args) > 0 && args[0] == "__complete" {
		return runComplete(ctx, args[1:], stdout)
	}

	flags := pflag.NewFlagSet("kubectl-liken-media", pflag.ContinueOnError)
	flags.SetOutput(stderr)
	// pflag's own usage prints only the flags. printHelp prints the
	// verbs and an example first.
	flags.Usage = func() {}
	printHelp := func() {
		fmt.Fprintln(stderr, usageText)
		fmt.Fprintf(stderr, "\nFlags:\n%s", flags.FlagUsages())
	}

	showVersion := flags.Bool("version", false, versionUsage)
	format := flags.String("format", "mp4", formatUsage)
	force := flags.Bool("force", false, forceUsage)

	// cli-runtime turns these flags into a REST config. Only kubeconfig,
	// context, and namespace are registered.
	config := genericclioptions.NewConfigFlags(true)
	flags.StringVar(config.KubeConfig, "kubeconfig", *config.KubeConfig, kubeconfigUsage)
	flags.StringVar(config.Context, "context", *config.Context, contextUsage)
	flags.StringVarP(config.Namespace, "namespace", "n", *config.Namespace, namespaceUsage)

	if err := flags.Parse(args); err != nil {
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
	case "capture":
		return runCapture(ctx, config, captureOptions{
			Namespace: *config.Namespace,
			Name:      argAt(positional, 1),
			Format:    *format,
			Force:     *force,
		}, stdout, stderr)
	case "completion":
		return completionScript(argAt(positional, 1), stdout)
	default:
		return fmt.Errorf("unknown verb %q, want capture or completion", positional[0])
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
