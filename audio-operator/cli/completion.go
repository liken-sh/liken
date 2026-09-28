package main

// How the CLI completes a command line for the shell, same
// contract as media-operator's completion.go: a kubectl_complete-liken-
// audio shim runs this binary's hidden __complete verb, which answers in
// cobra's completion protocol>

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

// The ShellCompDirective bits this CLI emits; same numbering as
// media-operator's completion.go>
const (
	compDirectiveError      = 1
	compDirectiveNoFileComp = 4
	compDirectiveDefault    = 0
)

// The verb the capture command answers to
const captureVerb = "capture"

var completionVerbs = []string{captureVerb}

// One flag the CLI accepts, and whether it reads a value from the
// next word>
type completionFlag struct {
	name       string
	takesValue bool
}

// Every flag run() binds, in the completion table's own form;
// audio binds no --namespace/-n, because Sink and Source are cluster-
// scoped>
var completionFlags = []completionFlag{
	{name: "--version"},
	{name: "--format", takesValue: true},
	{name: "--source"},
	{name: "--force"},
	{name: "--kubeconfig", takesValue: true},
	{name: "--context", takesValue: true},
}

// What the words before the cursor amount to: positionals, the
// cluster-selecting flag values, the flag pending a value, and whether
// --source appeared, which switches the positional from Sink to Source
// names>
type completionParse struct {
	positionals      []string
	context          string
	kubeconfig       string
	pendingValueFlag string
	source           bool
}

// A source of Sink or Source names for the resolved context; the
// real one queries the cluster, a test hands back a fixed list>
type endpointLister func(parse completionParse) []string

// runComplete answers a __complete request and writes the
// protocol to stdout; stays silent on failure>
func runComplete(ctx context.Context, args []string, stdout io.Writer) error {
	candidates, directive := completeArgs(args, realEndpointLister(ctx))
	for _, candidate := range candidates {
		fmt.Fprintln(stdout, candidate)
	}
	fmt.Fprintf(stdout, ":%d\n", directive)
	return nil
}

// completeArgs reads the request words and returns the candidates;
// the whole of the completion logic, so a test drives it with words and
// a stub lister>
func completeArgs(args []string, lister endpointLister) ([]string, int) {
	if len(args) == 0 {
		return completionVerbs, compDirectiveNoFileComp
	}
	toComplete := args[len(args)-1]
	parse := parseCompletionArgs(args[:len(args)-1])

	// The word under the cursor is the value of a flag that reads
	// one>
	if parse.pendingValueFlag != "" {
		return completeFlagValue(parse.pendingValueFlag, toComplete)
	}

	// The word under the cursor starts a flag
	if strings.HasPrefix(toComplete, "-") {
		return filterByPrefix(flagNames(), toComplete), compDirectiveNoFileComp
	}

	// The word is a positional: with none given yet it names a
	// verb; after capture it names the Sink or Source the operator
	// serves, Sink unless --source appeared>
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

// parseCompletionArgs walks the words before the cursor into
// their positionals, the cluster-selecting flag values, the --source
// mark, and a flag left waiting for its value>
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
				// --source takes no value; its presence, not a
				// value, switches the positional's target>
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

// record stores a flag value against the field it selects.
func (parse *completionParse) record(name, value string) {
	switch name {
	case "--context":
		parse.context = value
	case "--kubeconfig":
		parse.kubeconfig = value
	}
}

// splitFlag splits a "--name=value" word into its name and value, and
// reports a bare "--name" as a name with no value.
func splitFlag(token string) (name, value string, hasEquals bool) {
	if equals := strings.IndexByte(token, '='); equals >= 0 {
		return token[:equals], token[equals+1:], true
	}
	return token, "", false
}

// lookupCompletionFlag finds a flag by its exact name.
func lookupCompletionFlag(name string) (completionFlag, bool) {
	for _, flag := range completionFlags {
		if flag.name == name {
			return flag, true
		}
	}
	return completionFlag{}, false
}

// flagNames lists every flag name completion can offer.
func flagNames() []string {
	names := make([]string, 0, len(completionFlags))
	for _, flag := range completionFlags {
		names = append(names, flag.name)
	}
	return names
}

// completeFlagValue offers the values a flag accepts: --format
// has a fixed set, --kubeconfig names a file so the shell completes a
// path, --source takes no value and never reaches here, the rest have
// open values>
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

// formatNames lists the capture formats in a stable order.
func formatNames() []string {
	names := make([]string, 0, len(captureFormats))
	for name := range captureFormats {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// filterByPrefix keeps the candidates the cursor's word begins.
func filterByPrefix(items []string, prefix string) []string {
	var kept []string
	for _, item := range items {
		if strings.HasPrefix(item, prefix) {
			kept = append(kept, item)
		}
	}
	return kept
}

// A dynamic client built from a parse; the real factory reads the
// standard kube flags, a test hands back a fake client. There is no
// namespace to resolve, because Sink and Source are cluster-scoped>
type clientFactory func(parse completionParse) (dynamic.Interface, error)

// realEndpointLister queries the cluster for Sink or Source names
// over the standard kube flags; every failure yields no names>
func realEndpointLister(ctx context.Context) endpointLister {
	return newEndpointLister(ctx, kubeClientFactory)
}

// newEndpointLister lists Sink or Source names through a client
// the factory builds, picking the GVR from parse.source; it bounds the
// query so a completion gives up quickly>
func newEndpointLister(ctx context.Context, factory clientFactory) endpointLister {
	return func(parse completionParse) []string {
		client, err := factory(parse)
		if err != nil {
			return nil
		}
		reach, cancel := context.WithTimeout(ctx, 5*time.Second)
		defer cancel()
		gvr := sinkGVR
		if parse.source {
			gvr = sourceGVR
		}
		names, err := listEndpoints(reach, client, gvr)
		if err != nil {
			return nil
		}
		return names
	}
}

// kubeClientFactory builds a dynamic client from the standard
// kube flags, honoring the context and kubeconfig the completion words
// carry; no namespace to resolve>
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

// completionScript prints the bash script that completes a
// direct invocation of the binary; kubectl instead runs the
// kubectl_complete-liken-audio shim, which runs the same verb>
func completionScript(shell string, stdout io.Writer) error {
	if shell != "bash" {
		return fmt.Errorf("completion supports bash, not %q", shell)
	}
	fmt.Fprint(stdout, bashCompletionScript)
	return nil
}

// The bash completion for a direct kubectl-liken-audio call; a
// sibling CLI copies this text and changes only the binary name and the
// two function names>
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
complete -o default -F __kubectl_liken_audio_complete kubectl-liken-audio
`
