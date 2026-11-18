package main

// Shell completion for the CLI. kubectl runs the plugin's
// kubectl_complete-liken-observatory shim, which runs this binary's
// hidden __complete verb. The verb prints in cobra's completion
// protocol: one candidate per line, then ":N", where N is the
// ShellCompDirective bitmask.

import (
	"context"
	"fmt"
	"io"
	"strings"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/cli-runtime/pkg/genericclioptions"
	"k8s.io/client-go/dynamic"
)

// The cobra ShellCompDirective values that this CLI prints.
const (
	compDirectiveError      = 1
	compDirectiveNoFileComp = 4
	compDirectiveDefault    = 0
)

// completionVerbs are the verbs that completion offers. Both take a kind
// and a name.
var completionVerbs = []string{setUpVerb, takeDownVerb}

// completionFlag is a flag the CLI accepts, and whether the word after
// it is its value.
type completionFlag struct {
	name       string
	takesValue bool
}

// completionFlags must list every flag that run binds. run passes a
// completion request to runComplete before it builds its flag set, so
// completion keeps its own list.
var completionFlags = []completionFlag{
	{name: "--version"},
	{name: "--until", takesValue: true},
	{name: "--by", takesValue: true},
	{name: "--force"},
	{name: "--kubeconfig", takesValue: true},
	{name: "--context", takesValue: true},
	{name: "--namespace", takesValue: true},
	{name: "-n", takesValue: true},
}

// completionParse holds what the words before the cursor set: the
// positional arguments, the flags that select a cluster, and
// pendingValueFlag, a flag that ends the words and takes the word under
// the cursor as its value.
type completionParse struct {
	positionals      []string
	namespace        string
	context          string
	kubeconfig       string
	pendingValueFlag string
}

// resourceLister returns the names of the resources of one kind in the
// namespace and context that the parse selects. Tests replace the real
// one, which queries the cluster, with a fixed list.
type resourceLister func(parse completionParse, kind confirmable) []string

// runComplete handles a __complete request. It prints one candidate per
// line and then the directive, and returns no error, so a failed
// completion prints no message into the user's shell.
func runComplete(ctx context.Context, args []string, stdout io.Writer) error {
	candidates, directive := completeArgs(args, realResourceLister(ctx))
	for _, candidate := range candidates {
		fmt.Fprintln(stdout, candidate)
	}
	fmt.Fprintf(stdout, ":%d\n", directive)
	return nil
}

// completeArgs returns the candidates and the directive for the words
// of a request, where the last word is the one under the cursor. It
// takes the lister as a parameter so a test needs no cluster.
func completeArgs(args []string, lister resourceLister) ([]string, int) {
	if len(args) == 0 {
		return completionVerbs, compDirectiveNoFileComp
	}
	toComplete := args[len(args)-1]
	parse := parseCompletionArgs(args[:len(args)-1])

	// The word under the cursor is the value of the flag before it.
	if parse.pendingValueFlag != "" {
		return completeFlagValue(parse.pendingValueFlag, toComplete)
	}

	// The word under the cursor starts a flag name.
	if strings.HasPrefix(toComplete, "-") {
		return filterByPrefix(flagNames(), toComplete), compDirectiveNoFileComp
	}

	// The word is a positional argument. First it is a verb, then a
	// kind, then the name of a resource of that kind.
	verb := len(parse.positionals) > 0 && (parse.positionals[0] == setUpVerb || parse.positionals[0] == takeDownVerb)
	switch {
	case len(parse.positionals) == 0:
		return filterByPrefix(completionVerbs, toComplete), compDirectiveNoFileComp
	case verb && len(parse.positionals) == 1:
		return filterByPrefix(kindWords(), toComplete), compDirectiveNoFileComp
	case verb && len(parse.positionals) == 2:
		kind, err := kindNamed(parse.positionals[1])
		if err != nil {
			return nil, compDirectiveNoFileComp
		}
		return filterByPrefix(lister(parse, kind), toComplete), compDirectiveNoFileComp
	}
	return nil, compDirectiveNoFileComp
}

// parseCompletionArgs sorts the words before the cursor into positional
// arguments and the values of --context, --kubeconfig, and --namespace.
// A value flag that ends the words becomes pendingValueFlag.
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

// record stores the value of a flag that selects a cluster.
func (parse *completionParse) record(name, value string) {
	switch name {
	case "--context":
		parse.context = value
	case "--kubeconfig":
		parse.kubeconfig = value
	case "--namespace", "-n":
		parse.namespace = value
	}
}

// splitFlag splits "--name=value" into name and value. For a word with
// no "=" it returns the word and false.
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

// completeFlagValue offers no candidates for a flag value. The
// directive for --kubeconfig lets the shell complete file paths. The
// other flags take values that no list can predict.
func completeFlagValue(flag, toComplete string) ([]string, int) {
	switch flag {
	case "--kubeconfig":
		return nil, compDirectiveDefault
	default:
		return nil, compDirectiveNoFileComp
	}
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

// clientFactory returns a dynamic client and the namespace to list in,
// for the cluster that the parse selects. Tests replace the real one
// with a factory that returns a fake client.
type clientFactory func(parse completionParse) (dynamic.Interface, string, error)

// realResourceLister lists resource names from the cluster that the
// standard kube flags select. Any failure returns no names.
func realResourceLister(ctx context.Context) resourceLister {
	return newResourceLister(ctx, kubeClientFactory)
}

// newResourceLister lists the names of the resources of a kind through
// the client that the factory builds. The 5 second timeout keeps an
// unreachable cluster from freezing the user's shell.
func newResourceLister(ctx context.Context, factory clientFactory) resourceLister {
	return func(parse completionParse, kind confirmable) []string {
		client, namespace, err := factory(parse)
		if err != nil {
			return nil
		}
		listCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
		defer cancel()
		list, err := client.Resource(kind.resource()).Namespace(namespace).List(listCtx, metav1.ListOptions{})
		if err != nil {
			return nil
		}
		names := make([]string, 0, len(list.Items))
		for _, item := range list.Items {
			names = append(names, item.GetName())
		}
		return names
	}
}

// kubeClientFactory builds a dynamic client from the standard kube
// flags, with the context, kubeconfig, and namespace that the words
// before the cursor set. The namespace falls back to the kubeconfig's.
func kubeClientFactory(parse completionParse) (dynamic.Interface, string, error) {
	flags := genericclioptions.NewConfigFlags(true)
	if parse.context != "" {
		*flags.Context = parse.context
	}
	if parse.kubeconfig != "" {
		*flags.KubeConfig = parse.kubeconfig
	}
	namespace := parse.namespace
	if namespace == "" {
		resolved, _, err := flags.ToRawKubeConfigLoader().Namespace()
		if err != nil {
			return nil, "", err
		}
		namespace = resolved
	}
	config, err := flags.ToRESTConfig()
	if err != nil {
		return nil, "", err
	}
	client, err := dynamic.NewForConfig(config)
	if err != nil {
		return nil, "", err
	}
	return client, namespace, nil
}

// completionScript prints the bash script for running the binary
// directly. kubectl does not use it, because it runs the
// kubectl_complete-liken-observatory shim.
func completionScript(shell string, stdout io.Writer) error {
	if shell != "bash" {
		return fmt.Errorf("unknown shell %q, want bash", shell)
	}
	fmt.Fprint(stdout, bashCompletionScript)
	return nil
}

// bashCompletionScript completes a direct kubectl-liken-observatory
// call. The sibling CLIs use the same text with their own binary name
// and function names.
const bashCompletionScript = `# bash completion for kubectl-liken-observatory
__kubectl_liken_observatory_complete()
{
    local cur args out comp directive
    COMPREPLY=()
    cur="${COMP_WORDS[COMP_CWORD]}"

    # Send the hidden __complete verb the words after the binary name,
    # ending with the word under the cursor.
    args=("${COMP_WORDS[@]:1:COMP_CWORD-1}")
    out="$("${COMP_WORDS[0]}" __complete "${args[@]}" "$cur" 2>/dev/null)"

    # The last line is ":N", the ShellCompDirective bitmask.
    directive="${out##*$'\n'}"
    directive="${directive#:}"
    [[ "$directive" =~ ^[0-9]+$ ]] || directive=0

    # Every other line is a candidate. A tab and a description can
    # follow the value, so keep only the value.
    while IFS= read -r comp; do
        [[ -z "$comp" || "$comp" == :* ]] && continue
        COMPREPLY+=("${comp%%$'\t'*}")
    done <<< "${out%$'\n'*}"

    # Bit 2 adds no space after the candidate. Bit 4 turns off the file
    # name fallback that "complete -o default" would otherwise add.
    (( (directive & 2) != 0 )) && compopt -o nospace 2>/dev/null
    (( (directive & 4) != 0 )) && compopt +o default 2>/dev/null
}
complete -o default -F __kubectl_liken_observatory_complete kubectl-liken-observatory
`
