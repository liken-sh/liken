package main

// kubectl-liken-audio is the CLI plugin of audio-operator. kubectl
// finds it on PATH and runs "kubectl liken audio capture room" as this
// binary with the arguments "capture room".

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"

	"github.com/spf13/pflag"
	"k8s.io/cli-runtime/pkg/genericclioptions"
)

// A release build sets version with -ldflags "-X main.version=...".
// Other builds report "dev".
var version = "dev"

const (
	usageText = `kubectl liken audio: stream the sound of a Sink or a Source

Verbs:
  capture <name>     stream the sound of a Sink, or of a Source with --source, to stdout as WAV, FLAC, or Opus
  completion bash    print the bash completion script

Example:
  kubectl liken audio capture living-room > living-room.wav`
	formatUsage     = "audio format of the stream: wav, flac, or opus"
	sourceUsage     = "treat the name as a Source, not a Sink"
	forceUsage      = "do not warn when the CLI version differs from the operator version"
	versionUsage    = "print the version of this CLI"
	kubeconfigUsage = "path to the kubeconfig file"
	contextUsage    = "the kubeconfig context to use"
)

// programName starts each message that this binary prints, spelled as
// a person types the command.
const programName = "kubectl liken audio"

// verbList must match the verbs of the switch in run.
const verbList = "capture and completion"

func main() {
	if err := run(context.Background(), os.Args[1:], os.Stdout, os.Stderr); err != nil {
		fmt.Fprintf(os.Stderr, "%s: %v\n", programName, err)
		os.Exit(1)
	}
}

// main only calls run, so a test calls run with its own arguments and
// writers.
func run(ctx context.Context, args []string, stdout, stderr io.Writer) error {
	// __complete runs before flag parsing because a completion request
	// can end in a partial flag or an empty word, which pflag rejects.
	if len(args) > 0 && args[0] == "__complete" {
		return runComplete(ctx, args[1:], stdout)
	}

	flags := pflag.NewFlagSet("kubectl-liken-audio", pflag.ContinueOnError)
	flags.SetOutput(stderr)
	// pflag's own usage prints only the flags, so it is silenced, and
	// printHelp prints the verbs and an example before the flags.
	flags.Usage = func() {}
	printHelp := func() {
		fmt.Fprintln(stderr, usageText)
		fmt.Fprintf(stderr, "\nFlags:\n%s", flags.FlagUsages())
	}

	showVersion := flags.Bool("version", false, versionUsage)
	format := flags.String("format", "wav", formatUsage)
	source := flags.Bool("source", false, sourceUsage)
	force := flags.Bool("force", false, forceUsage)

	// cli-runtime's ConfigFlags turns the kube flags into a REST config.
	// Only --context and --kubeconfig are bound, to keep the help short.
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
			Source: *source,
			Format: *format,
			Force:  *force,
		}, stdout, stderr)
	case "completion":
		return completionScript(argAt(positional, 1), stdout)
	default:
		return fmt.Errorf("unknown verb %q: the verbs are %s", positional[0], verbList)
	}
}

func argAt(positional []string, index int) string {
	if index < len(positional) {
		return positional[index]
	}
	return ""
}
