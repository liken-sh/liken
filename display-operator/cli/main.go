package main

// kubectl-liken-display is the display operator's kubectl plugin.
// kubectl finds it on PATH, so "kubectl liken display capture
// lg-hdr-wqhd-display" runs this binary with the arguments "capture
// lg-hdr-wqhd-display".

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
// with -ldflags "-X main.version=...", and any other build reports
// "dev".
var version = "dev"

const (
	usageText = `kubectl liken display: capture what a display output shows

Verbs:
  capture <output>   stream the output's framebuffer to stdout as MP4, or PNG with --format
  completion bash    print the bash completion script

Example:
  kubectl liken display capture hdmi-a-1 | mpv -`
	formatUsage     = "capture format, mp4 or png"
	forceUsage      = "run despite a version mismatch"
	versionUsage    = "print the version"
	kubeconfigUsage = "path to the kubeconfig file"
	contextUsage    = "the kubeconfig context to use"
)

func main() {
	if err := run(context.Background(), os.Args[1:], os.Stdout, os.Stderr); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

// run parses args and runs the verb. main only wraps it, so a test can
// call it with its own arguments and writers.
func run(ctx context.Context, args []string, stdout, stderr io.Writer) error {
	// A shell completion request can end in a partial flag or an empty
	// word, which the flag parser would reject, so it is handled before
	// parsing.
	if len(args) > 0 && args[0] == "__complete" {
		return runComplete(ctx, args[1:], stdout)
	}

	flags := pflag.NewFlagSet("kubectl-liken-display", pflag.ContinueOnError)
	flags.SetOutput(stderr)
	// printHelp prints the verbs and an example before the flags, so
	// pflag's own usage, which prints only the flags, is turned off.
	flags.Usage = func() {}
	printHelp := func() {
		fmt.Fprintln(stderr, usageText)
		fmt.Fprintf(stderr, "\nFlags:\n%s", flags.FlagUsages())
	}

	showVersion := flags.Bool("version", false, versionUsage)
	format := flags.String("format", "mp4", formatUsage)
	force := flags.Bool("force", false, forceUsage)

	// cli-runtime turns the kubeconfig flags into a REST config. Only
	// --kubeconfig and --context are bound, to keep the help short.
	config := genericclioptions.NewConfigFlags(true)
	flags.StringVar(config.KubeConfig, "kubeconfig", *config.KubeConfig, kubeconfigUsage)
	flags.StringVar(config.Context, "context", *config.Context, contextUsage)

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
			Name:   argAt(positional, 1),
			Format: *format,
			Force:  *force,
		}, stdout, stderr)
	case "completion":
		return completionScript(argAt(positional, 1), stdout)
	default:
		return fmt.Errorf("unknown verb %q, want capture or completion", positional[0])
	}
}

func argAt(positional []string, index int) string {
	if index < len(positional) {
		return positional[index]
	}
	return ""
}
