package main

// How the CLI completes a command line for the shell, same
// contract as media-operator's completion.go: a kubectl_complete-liken-
// library shim runs this binary's hidden __complete verb, which answers
// in cobra's completion protocol>

import (
	"context"
	"fmt"
	"io"
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

// The verbs reenrich and rescan answer to; both take the same
// positional, a Library name, so completion treats them alike everywhere
// but the verb word itself>
const (
	reenrichVerb = "reenrich"
	rescanVerb   = "rescan"
)

var completionVerbs = []string{reenrichVerb, rescanVerb}

// One flag the CLI accepts, and whether it reads a value from the
// next word>
type completionFlag struct {
	name       string
	takesValue bool
}

// Every flag run() binds, in the completion table's own form
var completionFlags = []completionFlag{
	{name: "--version"},
	{name: "--only", takesValue: true},
	{name: "--force"},
	{name: "--kubeconfig", takesValue: true},
	{name: "--context", takesValue: true},
	{name: "--namespace", takesValue: true},
	{name: "-n", takesValue: true},
}

// What the words before the cursor amount to: positionals, the
// cluster-selecting flag values, and the flag pending a value>
type completionParse struct {
	positionals      []string
	namespace        string
	context          string
	kubeconfig       string
	pendingValueFlag string
}

// A source of Library names for the resolved namespace and
// context; the real one queries the cluster, a test hands back a fixed
// list>
type libraryLister func(parse completionParse) []string

// runComplete answers a __complete request and writes the
// protocol to stdout; stays silent on failure>
func runComplete(ctx context.Context, args []string, stdout io.Writer) error {
	candidates, directive := completeArgs(args, realLibraryLister(ctx))
	for _, candidate := range candidates {
		fmt.Fprintln(stdout, candidate)
	}
	fmt.Fprintf(stdout, ":%d\n", directive)
	return nil
}

// completeArgs reads the request words and returns the candidates;
// the whole of the completion logic, so a test drives it with words and
// a stub lister>
func completeArgs(args []string, lister libraryLister) ([]string, int) {
	if len(args) == 0 {
		return completionVerbs, compDirectiveNoFileComp
	}
	toComplete := args[len(args)-1]
	parse := parseCompletionArgs(args[:len(args)-1])

	// The word under the cursor is the value of a flag that
	// reads one>
	if parse.pendingValueFlag != "" {
		return completeFlagValue(parse.pendingValueFlag, toComplete)
	}

	// The word under the cursor starts a flag
	if strings.HasPrefix(toComplete, "-") {
		return filterByPrefix(flagNames(), toComplete), compDirectiveNoFileComp
	}

	// The word is a positional: with none given yet it names a
	// verb; after reenrich or rescan it names the Library the verb works
	// on, so both verbs reach the same lister call>
	switch len(parse.positionals) {
	case 0:
		return filterByPrefix(completionVerbs, toComplete), compDirectiveNoFileComp
	case 1:
		if parse.positionals[0] == reenrichVerb || parse.positionals[0] == rescanVerb {
			return filterByPrefix(lister(parse), toComplete), compDirectiveNoFileComp
		}
	}
	return nil, compDirectiveNoFileComp
}

// parseCompletionArgs walks the words before the cursor into
// their positionals and the cluster-selecting flag values, and marks a
// flag left waiting for its value>
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

// record stores a flag value against the field it selects.
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

// completeFlagValue offers the values a flag accepts; --only has
// a fixed vocabulary in the operator's own pass order, offered unsorted;
// kubeconfig names a file so the shell completes a path; the rest have
// open values>
func completeFlagValue(flag, toComplete string) ([]string, int) {
	switch flag {
	case "--only":
		return filterByPrefix(refreshFactVocabulary, toComplete), compDirectiveNoFileComp
	case "--kubeconfig":
		return nil, compDirectiveDefault
	default:
		return nil, compDirectiveNoFileComp
	}
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

// A dynamic client for the resolved namespace, built from a
// parse; the real factory reads the standard kube flags, a test hands
// back a fake client>
type clientFactory func(parse completionParse) (dynamic.Interface, string, error)

// realLibraryLister queries the cluster for Library names over
// the standard kube flags; every failure yields no names>
func realLibraryLister(ctx context.Context) libraryLister {
	return newLibraryLister(ctx, kubeClientFactory)
}

// newLibraryLister lists Library names through a client the
// factory builds; it bounds the query so a completion gives up quickly>
func newLibraryLister(ctx context.Context, factory clientFactory) libraryLister {
	return func(parse completionParse) []string {
		client, namespace, err := factory(parse)
		if err != nil {
			return nil
		}
		reach, cancel := context.WithTimeout(ctx, 5*time.Second)
		defer cancel()
		names, err := listLibraries(reach, client, namespace)
		if err != nil {
			return nil
		}
		return names
	}
}

// kubeClientFactory builds a dynamic client from the standard
// kube flags, honoring the context, kubeconfig, and namespace the
// completion words carry>
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

// completionScript prints the bash script that completes a
// direct invocation of the binary; kubectl instead runs the
// kubectl_complete-liken-library shim, which runs the same verb>
func completionScript(shell string, stdout io.Writer) error {
	if shell != "bash" {
		return fmt.Errorf("completion supports bash, not %q", shell)
	}
	fmt.Fprint(stdout, bashCompletionScript)
	return nil
}

// The bash completion for a direct kubectl-liken-library call; a
// sibling CLI copies this text and changes only the binary name and the
// two function names>
const bashCompletionScript = `# bash completion for kubectl-liken-library
__kubectl_liken_library_complete()
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
complete -o default -F __kubectl_liken_library_complete kubectl-liken-library
`
