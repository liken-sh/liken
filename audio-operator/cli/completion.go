package main

// Shell completion for kubectl liken audio. kubectl runs the shim
// kubectl_complete-liken-audio, which calls this binary's hidden
// __complete verb. The verb prints one candidate per line and then
// ":<directive>", which is cobra's completion protocol.

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

// The cobra ShellCompDirective values that this CLI prints.
const (
	compDirectiveError      = 1
	compDirectiveNoFileComp = 4
	compDirectiveDefault    = 0
)

const captureVerb = "capture"

var completionVerbs = []string{captureVerb}

type completionFlag struct {
	name       string
	takesValue bool
}

// completionFlags must match the flags that run binds in main.go. There
// is no --namespace or -n because Sink and Source are cluster-scoped.
var completionFlags = []completionFlag{
	{name: "--version"},
	{name: "--format", takesValue: true},
	{name: "--source"},
	{name: "--force"},
	{name: "--kubeconfig", takesValue: true},
	{name: "--context", takesValue: true},
}

// pendingValueFlag is set when the last word before the cursor is a
// flag that takes a value, so the word under the cursor is that value.
// source is set by --source, which makes the argument after capture a
// Source name instead of a Sink name.
type completionParse struct {
	positionals      []string
	context          string
	kubeconfig       string
	pendingValueFlag string
	source           bool
}

// An endpointLister returns the Sink or Source names to offer.
// clusterEndpointLister reads them from the cluster, and a test returns
// a fixed list.
type endpointLister func(parse completionParse) []string

// When the names cannot be listed, runComplete writes no candidates and
// returns no error.
func runComplete(ctx context.Context, args []string, stdout io.Writer) error {
	candidates, directive := completeArgs(args, clusterEndpointLister(ctx))
	for _, candidate := range candidates {
		fmt.Fprintln(stdout, candidate)
	}
	fmt.Fprintf(stdout, ":%d\n", directive)
	return nil
}

// completeArgs returns the candidates and the directive for the last
// word of args. A test calls it with a stub lister in place of the
// cluster.
func completeArgs(args []string, lister endpointLister) ([]string, int) {
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

	// The first positional word is the verb. The word after capture is a
	// Sink name, or a Source name with --source.
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

func parseCompletionArgs(prior []string) completionParse {
	var parse completionParse
	for i := 0; i < len(prior); i++ {
		token := prior[i]
		if len(token) > 1 && token[0] == '-' {
			name, value, hasEquals := splitFlag(token)
			flag, ok := lookupCompletionFlag(name)
			if !ok {
				continue
			}
			if !flag.takesValue {
				if name == "--source" {
					parse.source = true
				}
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

// --kubeconfig takes a file path, so completeFlagValue returns
// compDirectiveDefault for it, which lets the shell complete file names.
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

// formatNames sorts the names because map iteration order is random.
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

// A clientFactory builds a dynamic client for the parsed words.
// kubeClientFactory reads the kube flags, and a test returns a fake
// client.
type clientFactory func(parse completionParse) (dynamic.Interface, error)

func clusterEndpointLister(ctx context.Context) endpointLister {
	return newEndpointLister(ctx, kubeClientFactory)
}

// The returned lister returns no names on any error. The list times
// out after 5 seconds, so an unreachable cluster does not hang the shell
// prompt.
func newEndpointLister(ctx context.Context, factory clientFactory) endpointLister {
	return func(parse completionParse) []string {
		client, err := factory(parse)
		if err != nil {
			return nil
		}
		listCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
		defer cancel()
		gvr := sinkGVR
		if parse.source {
			gvr = sourceGVR
		}
		names, err := listEndpoints(listCtx, client, gvr)
		if err != nil {
			return nil
		}
		return names
	}
}

// kubeClientFactory reads --context and --kubeconfig from the parsed
// words, because run handles __complete before it parses any flags.
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

// completionScript prints the bash script that completes a direct call
// of kubectl-liken-audio. Completion through kubectl does not use it,
// because kubectl runs the kubectl_complete-liken-audio shim.
func completionScript(shell string, stdout io.Writer) error {
	if shell != "bash" {
		return fmt.Errorf("completion supports bash, not %q", shell)
	}
	fmt.Fprint(stdout, bashCompletionScript)
	return nil
}

// Each sibling CLI has a copy of this script with its own binary name
// and function name.
const bashCompletionScript = `# bash completion for kubectl-liken-audio
__kubectl_liken_audio_complete()
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

    # Every other line is a candidate. A tab and a description can follow
    # the value, so keep only the value.
    while IFS= read -r comp; do
        [[ -z "$comp" || "$comp" == :* ]] && continue
        COMPREPLY+=("${comp%%$'\t'*}")
    done <<< "${out%$'\n'*}"

    # Bit 2 keeps the cursor against the completion. Bit 4 stops the file
    # completion that the "complete -o default" registration adds otherwise.
    (( (directive & 2) != 0 )) && compopt -o nospace 2>/dev/null
    (( (directive & 4) != 0 )) && compopt +o default 2>/dev/null
}
complete -o default -F __kubectl_liken_audio_complete kubectl-liken-audio
`
