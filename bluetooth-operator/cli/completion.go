package main

// Shell completion for the CLI. kubectl runs the
// `kubectl_complete-liken-bluetooth` shim, which runs this binary's
// hidden `__complete` verb. The verb prints cobra's completion
// protocol, which kubectl reads: one candidate per line, then a last
// line `:N` with the directive. The other operator CLIs complete the
// same way.

import (
	"context"
	"fmt"
	"io"
	"strings"
	"time"

	"k8s.io/cli-runtime/pkg/genericclioptions"
	"k8s.io/client-go/dynamic"
)

// The ShellCompDirective values that this CLI prints, numbered as in
// cobra.
const (
	compDirectiveError      = 1
	compDirectiveNoFileComp = 4
	compDirectiveDefault    = 0
)

const (
	pairVerb   = "pair"
	unpairVerb = "unpair"
)

var completionVerbs = []string{pairVerb, unpairVerb}

// completionFlag is a flag the CLI accepts, and whether it takes the
// next word as its value.
type completionFlag struct {
	name       string
	takesValue bool
}

// completionFlags lists the flags that run binds.
var completionFlags = []completionFlag{
	{name: "--version"},
	{name: "--adapter", takesValue: true},
	{name: "--window", takesValue: true},
	{name: "--force"},
	{name: "--kubeconfig", takesValue: true},
	{name: "--context", takesValue: true},
	{name: "--namespace", takesValue: true},
	{name: "-n", takesValue: true},
}

// completionParse is what completion reads from the words before the
// cursor.
type completionParse struct {
	positionals      []string
	namespace        string
	context          string
	kubeconfig       string
	pendingValueFlag string
}

// peripheralLister returns the names of the `Peripheral`s in the
// cluster that the parsed flags select.
type peripheralLister func(parse completionParse) []string

// runComplete prints the candidates and the directive of a `__complete`
// request. A failure to list the `Peripheral`s prints no candidates and
// returns no error, so the shell shows nothing instead of an error.
func runComplete(ctx context.Context, args []string, stdout io.Writer) error {
	candidates, directive := completeArgs(args, realPeripheralLister(ctx))
	for _, candidate := range candidates {
		fmt.Fprintln(stdout, candidate)
	}
	fmt.Fprintf(stdout, ":%d\n", directive)
	return nil
}

// completeArgs returns the candidates and the directive for the words
// of a request, where the last word is the one under the cursor.
func completeArgs(args []string, lister peripheralLister) ([]string, int) {
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

	// The second positional after unpair is a `Peripheral` name. After
	// pair it is an adapter name, which has no completion.
	switch len(parse.positionals) {
	case 0:
		return filterByPrefix(completionVerbs, toComplete), compDirectiveNoFileComp
	case 1:
		if parse.positionals[0] == unpairVerb {
			return filterByPrefix(lister(parse), toComplete), compDirectiveNoFileComp
		}
	}
	return nil, compDirectiveNoFileComp
}

// parseCompletionArgs splits the words before the cursor into
// positionals and the values of the flags that select a cluster. When
// the last word is a flag that still needs its value, it sets
// pendingValueFlag.
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

// completeFlagValue returns the default directive for `--kubeconfig`,
// so the shell completes a file path, and no candidates for the other
// flags.
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

// clientFactory builds a dynamic client and a namespace from the parsed
// flags. The lister does not use the namespace, because `Peripheral` is
// cluster-scoped.
type clientFactory func(parse completionParse) (dynamic.Interface, string, error)

func realPeripheralLister(ctx context.Context) peripheralLister {
	return newPeripheralLister(ctx, kubeClientFactory)
}

// newPeripheralLister returns a lister that returns no names on any
// failure. The list times out after 5 seconds, so a slow cluster does
// not hang the shell.
func newPeripheralLister(ctx context.Context, factory clientFactory) peripheralLister {
	return func(parse completionParse) []string {
		client, _, err := factory(parse)
		if err != nil {
			return nil
		}
		reach, cancel := context.WithTimeout(ctx, 5*time.Second)
		defer cancel()
		names, err := listPeripherals(reach, client)
		if err != nil {
			return nil
		}
		return names
	}
}

// kubeClientFactory builds a dynamic client with kubectl's loading
// rules and the context, kubeconfig, and namespace from the command
// line.
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

// completionScript prints the bash script that completes a direct call
// of `kubectl-liken-bluetooth`. kubectl does not use it, because kubectl
// runs the shim.
func completionScript(shell string, stdout io.Writer) error {
	if shell != "bash" {
		return fmt.Errorf("completion supports only bash, got shell %q", shell)
	}
	fmt.Fprint(stdout, bashCompletionScript)
	return nil
}

// The other operator CLIs print the same script with their own binary
// and function names.
const bashCompletionScript = `# bash completion for kubectl-liken-bluetooth
__kubectl_liken_bluetooth_complete()
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
complete -o default -F __kubectl_liken_bluetooth_complete kubectl-liken-bluetooth
`
