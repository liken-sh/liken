package main

// Shell completion for this CLI. The sibling CLIs copy this file.
//
// kubectl completes a plugin by running an executable named
// kubectl_complete-<plugin> from PATH. That shim runs this binary's
// hidden __complete verb, which writes cobra's completion protocol to
// stdout: one candidate per line, then a last line ":N" where N is the
// ShellCompDirective bitmask. This CLI does not use cobra, so this file
// writes the protocol itself.

import (
	"context"
	"fmt"
	"io"
	"sort"
	"strings"
	"time"

	"k8s.io/cli-runtime/pkg/genericclioptions"
	"k8s.io/client-go/dynamic"
)

// cobra's ShellCompDirective values. Default lets the shell offer file
// names, and NoFileComp stops it. This CLI never sends Error.
const (
	compDirectiveError      = 1
	compDirectiveNoFileComp = 4
	compDirectiveDefault    = 0
)

// The verbs completion offers. A sibling CLI edits this list to match
// its own commands.
const captureVerb = "capture"

var completionVerbs = []string{captureVerb}

// completionFlag is one flag the CLI accepts. takesValue lets
// completion tell a flag's value in the next word from a positional
// argument.
type completionFlag struct {
	name       string
	takesValue bool
}

// Every flag that run binds, and it must stay the same as that flag
// set. A Display is cluster-scoped, so there is no namespace flag.
var completionFlags = []completionFlag{
	{name: "--version"},
	{name: "--format", takesValue: true},
	{name: "--force"},
	{name: "--kubeconfig", takesValue: true},
	{name: "--context", takesValue: true},
}

// completionParse is the words before the cursor: the positional
// arguments, the --context and --kubeconfig values, and the flag, if
// any, whose value is the word under the cursor.
type completionParse struct {
	positionals      []string
	context          string
	kubeconfig       string
	pendingValueFlag string
}

// outputLister returns the names of the Displays in the cluster that
// the parsed flags select. A test passes a fixed list.
type outputLister func(parse completionParse) []string

// runComplete handles a __complete request. It never prints an error,
// because the shell would show the message inside the command line.
func runComplete(ctx context.Context, args []string, stdout io.Writer) error {
	candidates, directive := completeArgs(args, realOutputLister(ctx))
	for _, candidate := range candidates {
		fmt.Fprintln(stdout, candidate)
	}
	fmt.Fprintf(stdout, ":%d\n", directive)
	return nil
}

// completeArgs returns the candidates and the directive for a request.
// The last word in args is the one under the cursor.
func completeArgs(args []string, lister outputLister) ([]string, int) {
	if len(args) == 0 {
		return completionVerbs, compDirectiveNoFileComp
	}
	toComplete := args[len(args)-1]
	parse := parseCompletionArgs(args[:len(args)-1])

	if parse.pendingValueFlag != "" {
		return completeFlagValue(parse.pendingValueFlag, toComplete)
	}

	if strings.HasPrefix(toComplete, "-") {
		return filterByPrefix(flagNames(), toComplete), compDirectiveNoFileComp
	}

	// The first positional argument is a verb, and the one after
	// capture is the name of a Display.
	switch len(parse.positionals) {
	case 0:
		return filterByPrefix(completionVerbs, toComplete), compDirectiveNoFileComp
	case 1:
		if parse.positionals[0] == captureVerb {
			return filterByPrefix(lister(parse), toComplete), compDirectiveNoFileComp
		}
	}
	return nil, compDirectiveNoFileComp
}

// parseCompletionArgs sorts the words before the cursor into
// positional arguments and flag values.
func parseCompletionArgs(prior []string) completionParse {
	var parse completionParse
	for i := 0; i < len(prior); i++ {
		token := prior[i]
		if len(token) > 1 && token[0] == '-' {
			name, value, hasEquals := splitFlag(token)
			flag, ok := lookupCompletionFlag(name)
			if !ok || !flag.takesValue {
				continue
			}
			if hasEquals {
				parse.record(name, value)
				continue
			}
			if i+1 < len(prior) {
				parse.record(name, prior[i+1])
				i++
				continue
			}
			parse.pendingValueFlag = name
			continue
		}
		parse.positionals = append(parse.positionals, token)
	}
	return parse
}

// record keeps the value of --context or --kubeconfig and ignores
// every other flag.
func (parse *completionParse) record(name, value string) {
	switch name {
	case "--context":
		parse.context = value
	case "--kubeconfig":
		parse.kubeconfig = value
	}
}

func splitFlag(token string) (name, value string, hasEquals bool) {
	if equals := strings.IndexByte(token, '='); equals >= 0 {
		return token[:equals], token[equals+1:], true
	}
	return token, "", false
}

func lookupCompletionFlag(name string) (completionFlag, bool) {
	for _, flag := range completionFlags {
		if flag.name == name {
			return flag, true
		}
	}
	return completionFlag{}, false
}

func flagNames() []string {
	names := make([]string, 0, len(completionFlags))
	for _, flag := range completionFlags {
		names = append(names, flag.name)
	}
	return names
}

// completeFlagValue returns the values to offer for a flag. For
// --kubeconfig it returns the default directive, so the shell completes
// a file path.
func completeFlagValue(flag, toComplete string) ([]string, int) {
	switch flag {
	case "--format":
		return filterByPrefix(formatNames(), toComplete), compDirectiveNoFileComp
	case "--kubeconfig":
		return nil, compDirectiveDefault
	default:
		return nil, compDirectiveNoFileComp
	}
}

func formatNames() []string {
	names := make([]string, 0, len(captureFormats))
	for name := range captureFormats {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

func filterByPrefix(items []string, prefix string) []string {
	var kept []string
	for _, item := range items {
		if strings.HasPrefix(item, prefix) {
			kept = append(kept, item)
		}
	}
	return kept
}

// clientFactory builds a Kubernetes client from the parsed flags. A
// test passes one that returns a fake client.
type clientFactory func(parse completionParse) (dynamic.Interface, error)

func realOutputLister(ctx context.Context) outputLister {
	return newOutputLister(ctx, kubeClientFactory)
}

// newOutputLister returns an outputLister that lists Displays through
// a client from factory. It returns no names on any failure, because
// completion must not print an error. The query times out after 5
// seconds, because the user waits at the prompt.
func newOutputLister(ctx context.Context, factory clientFactory) outputLister {
	return func(parse completionParse) []string {
		client, err := factory(parse)
		if err != nil {
			return nil
		}
		queryCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
		defer cancel()
		names, err := listDisplays(queryCtx, client)
		if err != nil {
			return nil
		}
		return names
	}
}

// kubeClientFactory builds a dynamic client the way run does, with the
// --context and --kubeconfig values typed before the cursor, so
// completion lists the Displays of the cluster that the command would
// use.
func kubeClientFactory(parse completionParse) (dynamic.Interface, error) {
	flags := genericclioptions.NewConfigFlags(true)
	if parse.context != "" {
		*flags.Context = parse.context
	}
	if parse.kubeconfig != "" {
		*flags.KubeConfig = parse.kubeconfig
	}
	config, err := flags.ToRESTConfig()
	if err != nil {
		return nil, err
	}
	client, err := dynamic.NewForConfig(config)
	if err != nil {
		return nil, err
	}
	return client, nil
}

// completionScript writes the bash script that completes a direct call
// of kubectl-liken-display. kubectl does not use the script, because it
// runs the kubectl_complete-liken-display shim, which calls the same
// __complete verb.
func completionScript(shell string, stdout io.Writer) error {
	if shell != "bash" {
		return fmt.Errorf("no completion script for the shell %q, only for bash", shell)
	}
	fmt.Fprint(stdout, bashCompletionScript)
	return nil
}

// bashCompletionScript is the part of cobra's generated bash script
// that this CLI needs. A sibling CLI copies it and changes the binary
// name and the function name.
const bashCompletionScript = `# bash completion for kubectl-liken-display
__kubectl_liken_display_complete()
{
    local cur args out comp directive
    COMPREPLY=()
    cur="${COMP_WORDS[COMP_CWORD]}"

    # The words after the binary name and before the cursor, then the
    # word under it, go to the hidden __complete verb.
    args=("${COMP_WORDS[@]:1:COMP_CWORD-1}")
    out="$("${COMP_WORDS[0]}" __complete "${args[@]}" "$cur" 2>/dev/null)"

    # The last line is ":N", the ShellCompDirective bitmask.
    directive="${out##*$'\n'}"
    directive="${directive#:}"
    [[ "$directive" =~ ^[0-9]+$ ]] || directive=0

    # Every other line is a candidate; a tab and a description may follow
    # the value, so keep only the value.
    while IFS= read -r comp; do
        [[ -z "$comp" || "$comp" == :* ]] && continue
        COMPREPLY+=("${comp%%$'\t'*}")
    done <<< "${out%$'\n'*}"

    # Bit 2 keeps the cursor against the completion; bit 4 stops the file
    # fallback the "complete -o default" registration would otherwise add.
    (( (directive & 2) != 0 )) && compopt -o nospace 2>/dev/null
    (( (directive & 4) != 0 )) && compopt +o default 2>/dev/null
}
complete -o default -F __kubectl_liken_display_complete kubectl-liken-display
`
