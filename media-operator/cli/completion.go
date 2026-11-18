package main

// Shell completion for kubectl-liken-media. kubectl completes a plugin
// by running the executable kubectl_complete-liken-media from PATH, and
// that shim runs this binary's hidden __complete verb. The verb prints
// cobra's completion protocol without depending on cobra: one candidate
// per line, then a last line ":N" with the ShellCompDirective bitmask.

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

// ShellCompDirective values, numbered as cobra numbers them.
// NoFileComp stops the shell from completing file names when the CLI
// has no candidates. Default leaves file completion on.
const (
	compDirectiveError      = 1
	compDirectiveNoFileComp = 4
	compDirectiveDefault    = 0
)

const captureVerb = "capture"

var completionVerbs = []string{captureVerb}

// completionFlag records whether a flag takes the next word as its
// value, so completion can tell that value from a positional argument.
type completionFlag struct {
	name       string
	takesValue bool
}

// completionFlags lists the flags that run() defines. Keep it in step
// with them by hand, because completion does not read the flag set.
var completionFlags = []completionFlag{
	{name: "--version"},
	{name: "--format", takesValue: true},
	{name: "--force"},
	{name: "--kubeconfig", takesValue: true},
	{name: "--context", takesValue: true},
	{name: "--namespace", takesValue: true},
	{name: "-n", takesValue: true},
}

// completionParse is what completion reads from the words before the
// cursor. pendingValueFlag is the flag whose value the cursor word is,
// or empty.
type completionParse struct {
	positionals      []string
	namespace        string
	context          string
	kubeconfig       string
	pendingValueFlag string
}

// playerLister returns the names of the Players in the namespace and
// context that parse selects.
type playerLister func(parse completionParse) []string

// runComplete handles the __complete verb. It never prints an error,
// because the shell would insert the words into the command line.
func runComplete(ctx context.Context, args []string, stdout io.Writer) error {
	candidates, directive := completeArgs(args, realPlayerLister(ctx))
	for _, candidate := range candidates {
		fmt.Fprintln(stdout, candidate)
	}
	fmt.Fprintf(stdout, ":%d\n", directive)
	return nil
}

// completeArgs returns the candidates and the directive for the message of
// a completion request. The last word is the one under the cursor.
func completeArgs(args []string, lister playerLister) ([]string, int) {
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

	// The first positional is the verb, and the one after capture is a
	// Player name.
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

// parseCompletionArgs reads the positionals and the values of the
// cluster flags from the words before the cursor. It skips a flag that
// it does not know as if the flag takes no value, so the word after it
// counts as a positional.
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

// splitFlag splits "--name=value" at the first "=".
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

// completeFlagValue leaves the value of --kubeconfig to the shell's
// file completion, because it is a path.
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
	var matches []string
	for _, item := range items {
		if strings.HasPrefix(item, prefix) {
			matches = append(matches, item)
		}
	}
	return matches
}

// clientFactory returns a dynamic client and the namespace to list in,
// for the flags in parse.
type clientFactory func(parse completionParse) (dynamic.Interface, string, error)

func realPlayerLister(ctx context.Context) playerLister {
	return newPlayerLister(ctx, kubeClientFactory)
}

// newPlayerLister returns a playerLister that returns no names on any
// failure. The query times out after 5 seconds, so an unreachable
// cluster does not hang the shell.
func newPlayerLister(ctx context.Context, factory clientFactory) playerLister {
	return func(parse completionParse) []string {
		client, namespace, err := factory(parse)
		if err != nil {
			return nil
		}
		queryCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
		defer cancel()
		names, err := listPlayers(queryCtx, client, namespace)
		if err != nil {
			return nil
		}
		return names
	}
}

// kubeClientFactory applies the --context, --kubeconfig, and
// --namespace words before the cursor, so completion queries the
// cluster and namespace that the command would use.
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

// completionScript writes the completion script for a person who runs
// kubectl-liken-media directly, not through kubectl. It supports only
// bash.
func completionScript(shell string, stdout io.Writer) error {
	if shell != "bash" {
		return fmt.Errorf("the completion script supports only bash, not %q", shell)
	}
	fmt.Fprint(stdout, bashCompletionScript)
	return nil
}

// bashCompletionScript does the part of cobra's generated bash script
// that this CLI needs: it passes the words to __complete, reads the
// candidates, and applies the directive.
const bashCompletionScript = `# bash completion for kubectl-liken-media
__kubectl_liken_media_complete()
{
    local cur args out comp directive
    COMPREPLY=()
    cur="${COMP_WORDS[COMP_CWORD]}"

    # Pass the words after the binary name, then the cursor word, to
    # __complete.
    args=("${COMP_WORDS[@]:1:COMP_CWORD-1}")
    out="$("${COMP_WORDS[0]}" __complete "${args[@]}" "$cur" 2>/dev/null)"

    # The last line is ":N", the ShellCompDirective bitmask.
    directive="${out##*$'\n'}"
    directive="${directive#:}"
    [[ "$directive" =~ ^[0-9]+$ ]] || directive=0

    # Every other line is a candidate. Cobra may append a tab and a
    # description to it, so keep only the text before the tab.
    while IFS= read -r comp; do
        [[ -z "$comp" || "$comp" == :* ]] && continue
        COMPREPLY+=("${comp%%$'\t'*}")
    done <<< "${out%$'\n'*}"

    # Bit 2 (NoSpace) stops bash from adding a space after the candidate.
    # Bit 4 (NoFileComp) turns off the file fallback that the "-o default"
    # in the registration below would add.
    (( (directive & 2) != 0 )) && compopt -o nospace 2>/dev/null
    (( (directive & 4) != 0 )) && compopt +o default 2>/dev/null
}
complete -o default -F __kubectl_liken_media_complete kubectl-liken-media
`
