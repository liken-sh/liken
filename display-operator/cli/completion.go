package main

// How the CLI completes a command line for the shell, and the
// contract the sibling CLIs copy. kubectl completes a plugin by running
// an executable named kubectl_complete-<domain> on PATH, and that shim
// runs this binary's hidden __complete verb. The verb answers in cobra's
// completion protocol: one candidate per line, then a final ":N" line
// that carries the ShellCompDirective bitmask. A hand-rolled CLI joins
// that protocol with this one file and a one-line shim, and never takes
// on cobra to do it.

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

// The ShellCompDirective bits this CLI emits, cobra's own
// numbering. The default is 0. NoFileComp stops the shell from offering
// file names when the CLI has nothing to add, and Error marks a request
// the CLI could not answer.
const (
	compDirectiveError      = 1
	compDirectiveNoFileComp = 4
	compDirectiveDefault    = 0
)

// The verb the capture command answers to, and the whole verb
// vocabulary a sibling CLI edits to match its own commands.
const captureVerb = "capture"

var completionVerbs = []string{captureVerb}

// One flag the CLI accepts, and whether it reads a value from the
// next word. Completion walks the words with this table so it can tell a
// flag's value apart from a positional argument.
type completionFlag struct {
	name       string
	takesValue bool
}

// Every flag run() binds, in the completion table's own form. A
// Display is cluster-scoped, so this CLI binds no namespace flag, and
// the table holds none. A sibling CLI keeps this list beside the flag
// set it parses, so the two never drift.
var completionFlags = []completionFlag{
	{name: "--version"},
	{name: "--format", takesValue: true},
	{name: "--force"},
	{name: "--kubeconfig", takesValue: true},
	{name: "--context", takesValue: true},
}

// What the words before the cursor amount to: the positional
// arguments already given, the cluster-selecting flag values, and the
// one flag whose value the cursor is about to type.
type completionParse struct {
	positionals      []string
	context          string
	kubeconfig       string
	pendingValueFlag string
}

// A source of Display names for the resolved context. The real
// one queries the cluster; a test hands back a fixed list, so the
// routing logic tests without a cluster.
type outputLister func(parse completionParse) []string

// runComplete answers a __complete request and writes the protocol to
// stdout. It stays silent on every failure, because a completer that
// prints an error corrupts the command line the shell is drawing.
func runComplete(ctx context.Context, args []string, stdout io.Writer) error {
	candidates, directive := completeArgs(args, realOutputLister(ctx))
	for _, candidate := range candidates {
		fmt.Fprintln(stdout, candidate)
	}
	fmt.Fprintf(stdout, ":%d\n", directive)
	return nil
}

// completeArgs reads the request words and returns the candidates. The last
// word is the one under the cursor, and the words before it are already
// typed. It is the whole of the completion logic, so a test drives it
// with words and a stub lister.
func completeArgs(args []string, lister outputLister) ([]string, int) {
	if len(args) == 0 {
		return completionVerbs, compDirectiveNoFileComp
	}
	toComplete := args[len(args)-1]
	parse := parseCompletionArgs(args[:len(args)-1])

	// The word under the cursor is the value of a flag that reads
	// one, so complete that flag's values, not a positional.
	if parse.pendingValueFlag != "" {
		return completeFlagValue(parse.pendingValueFlag, toComplete)
	}

	// The word under the cursor starts a flag, so complete flag
	// names.
	if strings.HasPrefix(toComplete, "-") {
		return filterByPrefix(flagNames(), toComplete), compDirectiveNoFileComp
	}

	// The word is a positional. With none given yet it names a
	// verb; after the capture verb it names the output the operator
	// serves.
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

// parseCompletionArgs walks the words before the cursor into their
// positionals and the cluster-selecting flag values, and marks a flag
// left waiting for its value.
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

// completeFlagValue offers the values a flag accepts. The format flag has
// a fixed set; the kubeconfig flag names a file, so the shell completes a
// path; the rest have open values.
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

// A dynamic client built from a parse. The real factory reads the
// standard kube flags; a test hands back a fake client, so the lister's
// own logic tests without a cluster.
type clientFactory func(parse completionParse) (dynamic.Interface, error)

// realOutputLister queries the cluster for Display names over the
// standard kube flags. Every failure yields no names, because completion
// must not stall or speak.
func realOutputLister(ctx context.Context) outputLister {
	return newOutputLister(ctx, kubeClientFactory)
}

// newOutputLister lists Display names through a client the factory
// builds. It bounds the query, because a completion must be quick and
// then give up with no names.
func newOutputLister(ctx context.Context, factory clientFactory) outputLister {
	return func(parse completionParse) []string {
		client, err := factory(parse)
		if err != nil {
			return nil
		}
		reach, cancel := context.WithTimeout(ctx, 5*time.Second)
		defer cancel()
		names, err := listDisplays(reach, client)
		if err != nil {
			return nil
		}
		return names
	}
}

// kubeClientFactory builds a dynamic client from the standard kube flags,
// honoring the context and kubeconfig the completion words carry, so a
// completion reaches the same cluster the command would.
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

// completionScript prints the bash script that completes a direct
// invocation of the binary. The shell sources it, and from then on it
// runs the hidden __complete verb and reads the protocol back. kubectl
// needs none of this: it runs the kubectl_complete-liken-display shim
// instead, which runs the same verb.
func completionScript(shell string, stdout io.Writer) error {
	if shell != "bash" {
		return fmt.Errorf("completion supports bash, not %q", shell)
	}
	fmt.Fprint(stdout, bashCompletionScript)
	return nil
}

// The bash completion for a direct kubectl-liken-display call. It
// mirrors the small part of cobra's generated script this CLI needs: run
// __complete with the words before the cursor and the word under it, read
// the candidates, and read the final ":N" directive. A sibling CLI copies
// this text and changes only the binary name and the two function names.
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
