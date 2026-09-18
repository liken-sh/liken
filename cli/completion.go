package main

// How the base CLI completes a command line for the shell, and
// the contract the per-operator CLIs copy. kubectl completes a plugin
// by running an executable named kubectl_complete-<name> on PATH, and
// that shim runs this binary's hidden __complete verb. The verb answers
// in cobra's completion protocol: one candidate per line, then a final
// ":N" line that carries the ShellCompDirective bitmask. The base CLI
// answers from static lists, because its verbs, its plugins
// subcommands, and its plugin domains are all known without a cluster.

import (
	"fmt"
	"io"
	"sort"
	"strings"
)

// The ShellCompDirective bits this CLI emits, cobra's own
// numbering. The default is 0, which leaves the shell's own file
// completion in place. NoFileComp stops the shell from offering file
// names when the CLI has a closed set of its own, and Error marks a
// request the CLI could not answer.
const (
	compDirectiveError      = 1
	compDirectiveNoFileComp = 4
	compDirectiveDefault    = 0
)

// The plugins group, the one verb with subcommands of its own.
const pluginsVerb = "plugins"

// Every top-level verb the dispatcher answers, minus completion
// and __complete, which serve the shell and never a person. A new verb
// in run() joins this list so the shell offers it.
var topLevelVerbs = []string{
	"new", "mint", "adopt", "kubeconfig", "kubectl", "stern", "flux",
	"approve-reboot", "request-reboot", "plugins", "layer", "fetch",
	"media", "stick", "bundle", "index", "serve", "version",
}

// The plugin domains the walk dispatches to a
// kubectl-liken-<domain> binary. The list is static because the base
// CLI ships with a release and completes before any cluster is reached,
// so the shell offers a domain the same whether its CLI is installed or
// not.
var pluginDomains = []string{"audio", "bluetooth", "display", "library", "media"}

// The plugins group's own subcommands.
var pluginsSubcommands = []string{"list", "remove", "sync"}

// topLevelCandidates is the verbs and the plugin domains as one
// sorted set with no repeat, so "liken <TAB>" offers a command and a
// domain side by side. media names both a verb and a domain, and it
// appears once.
func topLevelCandidates() []string {
	seen := map[string]bool{}
	var all []string
	for _, item := range topLevelVerbs {
		if !seen[item] {
			seen[item] = true
			all = append(all, item)
		}
	}
	for _, item := range pluginDomains {
		if !seen[item] {
			seen[item] = true
			all = append(all, item)
		}
	}
	sort.Strings(all)
	return all
}

// One flag the CLI accepts, and whether it reads a value from the
// next word. Completion walks the words with this table so it can tell a
// flag's value apart from a positional argument.
type completionFlag struct {
	name       string
	takesValue bool
}

// Every flag the subcommands bind, in the completion table's own
// form. The base CLI parses each subcommand with the standard flag
// package, which reads one and two dashes alike, so the table names the
// one-dash spelling the usage prints. Each of these reads a value.
var completionFlags = []completionFlag{
	{name: "-server", takesValue: true},
	{name: "-digest", takesValue: true},
	{name: "-source", takesValue: true},
	{name: "-slot-size", takesValue: true},
	{name: "-console", takesValue: true},
}

// What the words before the cursor amount to: the positional
// arguments already given, and the one flag whose value the cursor is
// about to type.
type completionParse struct {
	positionals      []string
	pendingValueFlag string
}

// runComplete answers a __complete request and writes the protocol to
// stdout. It stays silent on every failure, because a completer that
// prints an error corrupts the command line the shell is drawing.
func runComplete(args []string, stdout io.Writer) error {
	candidates, directive := completeArgs(args)
	for _, candidate := range candidates {
		fmt.Fprintln(stdout, candidate)
	}
	fmt.Fprintf(stdout, ":%d\n", directive)
	return nil
}

// completeArgs reads the request words and returns the candidates. The last
// word is the one under the cursor, and the words before it are already
// typed. It is the whole of the completion logic, so a test drives it
// with words alone.
func completeArgs(args []string) ([]string, int) {
	if len(args) == 0 {
		return topLevelCandidates(), compDirectiveNoFileComp
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
	// verb or a plugin domain; after the plugins verb it names a plugins
	// subcommand; a verb's own argument is a path, so the shell's file
	// completion answers it.
	switch {
	case len(parse.positionals) == 0:
		return filterByPrefix(topLevelCandidates(), toComplete), compDirectiveNoFileComp
	case len(parse.positionals) == 1 && parse.positionals[0] == pluginsVerb:
		return filterByPrefix(pluginsSubcommands, toComplete), compDirectiveNoFileComp
	default:
		return nil, compDirectiveDefault
	}
}

// parseCompletionArgs walks the words before the cursor into their
// positionals, and marks a flag left waiting for its value.
func parseCompletionArgs(prior []string) completionParse {
	var parse completionParse
	for i := 0; i < len(prior); i++ {
		token := prior[i]
		if len(token) > 1 && token[0] == '-' {
			name, _, hasEquals := splitFlag(token)
			flag, ok := lookupCompletionFlag(name)
			if !ok || !flag.takesValue {
				continue
			}
			if hasEquals {
				continue
			}
			if i+1 < len(prior) {
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

// splitFlag splits a "-name=value" word into its name and value, and
// reports a bare "-name" as a name with no value. The standard flag
// package reads one and two dashes alike, so a "--name" spelling splits
// the same way.
func splitFlag(token string) (name, value string, hasEquals bool) {
	if equals := strings.IndexByte(token, '='); equals >= 0 {
		return token[:equals], token[equals+1:], true
	}
	return token, "", false
}

// lookupCompletionFlag finds a flag by its exact name, and reads a
// "--name" spelling as the "-name" the table holds.
func lookupCompletionFlag(name string) (completionFlag, bool) {
	name = "-" + strings.TrimLeft(name, "-")
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

// completeFlagValue offers the values a flag accepts. Every base
// flag names an address, a digest, a size, or a console, none of which
// this CLI can enumerate, so it offers nothing and leaves the file
// fallback off.
func completeFlagValue(flag, toComplete string) ([]string, int) {
	return nil, compDirectiveNoFileComp
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

// completionScript prints the bash script that completes a direct
// invocation of the binary, under the liken name and the kubectl-liken
// name both. The shell sources it, and from then on it runs the hidden
// __complete verb and reads the protocol back. kubectl needs none of
// this: it runs the kubectl_complete-liken shim instead, which runs the
// same verb.
func completionScript(shell string, stdout io.Writer) error {
	if shell != "bash" {
		return fmt.Errorf("completion supports bash, not %q", shell)
	}
	fmt.Fprint(stdout, bashCompletionScript)
	return nil
}

// The bash completion for a direct liken or kubectl-liken call. It
// mirrors the small part of cobra's generated script this CLI needs: run
// __complete with the words before the cursor and the word under it, read
// the candidates, and read the final ":N" directive. A per-operator CLI
// copies this text and changes only the binary name and the two function
// names.
const bashCompletionScript = `# bash completion for liken and kubectl-liken
__kubectl_liken_complete()
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
complete -o default -F __kubectl_liken_complete liken kubectl-liken
`
